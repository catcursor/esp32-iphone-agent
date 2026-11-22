/*
 * Reverse RTSP event channel for legacy AirPlay mirroring. The forceKeyFrame
 * message and SETUP negotiation are documented by regulad/UxPlay (rosella,
 * lib/raop_events.c). All socket state here belongs to the RTSP server task.
 * SPDX-License-Identifier: GPL-3.0-or-later
 */
#ifndef ESP_PLATFORM
#define _POSIX_C_SOURCE 200809L
#endif
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#ifdef ESP_PLATFORM
#include "lwip/sockets.h"
#include "esp_heap_caps.h"
#else
#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>
#endif
#include "../bplist/bplist.h"
#include "ap_internal.h"

enum { RETRY_MS = 5000, HEADER_MAX = 1024, BODY_MAX = 64 * 1024 };
struct ap_events {
  int fd;
  uint16_t port;
  bool connected, rejected, attempted;
  unsigned cseq;
  uint32_t last_send;
  char header[HEADER_MAX];
  size_t used, body_left;
  char tx[512];
  size_t tx_len, keyframe_bytes;
};

// Only the mirror task can confirm that a requested independent picture arrived.
// An RTSP 200 just acknowledges receipt of the request.
enum { HAS_FRAME = 1, NEED_IDR = 2, SENT_REQUEST = 4 };
static unsigned frame_state;
static bool available;

void ap_events_stream_reset(void) {
  __atomic_store_n(&frame_state, 0, __ATOMIC_RELAXED);
  __atomic_store_n(&available, false, __ATOMIC_RELAXED);
}

void ap_events_frame(bool idr) {
  if (idr) {
    unsigned previous = __atomic_exchange_n(&frame_state, HAS_FRAME, __ATOMIC_RELAXED);
    if (previous & SENT_REQUEST) ap_log("mirror: received keyframe after request");
  } else __atomic_fetch_or(&frame_state, HAS_FRAME, __ATOMIC_RELAXED);
}

void airplay_request_keyframe(void) {
  __atomic_fetch_or(&frame_state, NEED_IDR, __ATOMIC_RELAXED);
}

bool airplay_keyframe_supported(void) {
  return __atomic_load_n(&available, __ATOMIC_RELAXED);
}

static int listen_port(uint16_t *port) {
  struct sockaddr_in addr = {.sin_family = AF_INET, .sin_port = htons(*port), .sin_addr.s_addr = htonl(INADDR_ANY)};
  socklen_t n = sizeof addr;
  int fd = socket(AF_INET, SOCK_STREAM, 0), one = 1;
  if (fd < 0) return -1;
  setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
  if (bind(fd, (struct sockaddr *)&addr, n) || listen(fd, 1) || getsockname(fd, (struct sockaddr *)&addr, &n)) {
    close(fd);
    return -1;
  }
  *port = ntohs(addr.sin_port);
  return fd;
}

uint16_t ap_events_open(ap_conn *c) {
  if (c->events) return c->events->port;
#ifdef ESP_PLATFORM
  struct ap_events *e = heap_caps_calloc(1, sizeof *e, MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
#else
  struct ap_events *e = calloc(1, sizeof *e);
#endif
  if (!e) return 0;
  e->fd = listen_port(&e->port);
  if (e->fd < 0) { free(e); return 0; }
  c->events = e;
  return e->port;
}

void ap_events_close(ap_conn *c) {
  if (c == ap_mirror_owner) ap_events_stream_reset();
  if (!c->events) return;
  if (c->events->fd >= 0) close(c->events->fd);
  free(c->events);
  c->events = NULL;
}

int ap_events_fd(const ap_conn *c) {
  return c->events ? c->events->fd : -1;
}

static void accept_phone(ap_conn *c) {
  struct ap_events *e = c->events;
  struct sockaddr_in peer;
  socklen_t len = sizeof peer;
  int fd = accept(e->fd, (struct sockaddr *)&peer, &len);
  if (fd < 0) return;
  if (peer.sin_addr.s_addr != c->remote_ip || (ap_cfg.allow_peer && !ap_cfg.allow_peer(peer.sin_addr.s_addr))) {
    close(fd);
    return;
  }
  // Keep just the accepted socket until the phone disconnects, then reopen the advertised port.
  close(e->fd);
  e->fd = fd;
  e->connected = true;
  e->rejected = false;
  e->attempted = false;
  e->used = e->body_left = e->tx_len = e->keyframe_bytes = 0;
  ap_log("mirror: event channel connected");
}

static bool queue_keyframe_request(struct ap_events *e) {
  bp_writer w;
  bp_writer_init(&w);
  int dict = bp_w_dict(&w);
  bp_w_set(&w, dict, "type", bp_w_string(&w, "forceKeyFrame"));
  size_t n = 0;
  uint8_t *body = bp_w_finish(&w, &n);
  bp_writer_free(&w);
  if (!body) return false;
  int h = snprintf(e->tx, sizeof e->tx, "POST /command RTSP/1.0\r\nCSeq: %u\r\n"
                   "Content-Type: application/x-apple-binary-plist\r\nContent-Length: %u\r\n\r\n",
                   ++e->cseq, (unsigned)n);
  bool ok = h > 0 && (size_t)h < sizeof e->tx && n <= sizeof e->tx - (size_t)h;
  if (ok) {
    memcpy(e->tx + h, body, n);
    e->tx_len = e->keyframe_bytes = (size_t)h + n;
  }
  free(body);
  return ok;
}

static bool handle_header(struct ap_events *e) {
  char *save = NULL, *line = strtok_r(e->header, "\r\n", &save);
  if (!line) return false;
  bool reply = !strncmp(line, "RTSP/1.0 ", 9);
  int code = reply ? atoi(line + 9) : 0;
  if (!reply && !strstr(line, " RTSP/1.0")) return false;
  unsigned long cseq = 0;
  while ((line = strtok_r(NULL, "\r\n", &save))) {
    char *value = strchr(line, ':');
    if (!value) return false;
    *value++ = 0;
    if (!strcasecmp(line, "Content-Length") || !strcasecmp(line, "CSeq")) {
      char *end;
      errno = 0;
      unsigned long num = strtoul(value, &end, 10);
      if (end == value || *end || errno) return false;
      if (!strcasecmp(line, "CSeq")) cseq = num;
      else {
        if (num > BODY_MAX) return false;
        e->body_left = (size_t)num;
      }
    }
  }
  if (reply) {
    if (cseq == e->cseq && code >= 400 && code < 500) {
      e->rejected = true;
      ap_log("mirror: keyframe request rejected (%d)", code);
    }
    return code >= 100 && code < 600;
  }
  if (!cseq) return false;
  size_t room = sizeof e->tx - e->tx_len;
  int n = snprintf(e->tx + e->tx_len, room, "RTSP/1.0 200 OK\r\nCSeq: %lu\r\nContent-Length: 0\r\n\r\n", cseq);
  if (n <= 0 || (size_t)n >= room) return false;
  e->tx_len += (size_t)n;
  return true;
}

static bool read_event(struct ap_events *e) {
  char buf[512];
  int n = recv(e->fd, buf, sizeof buf, 0);
  if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR)) return true;
  if (n <= 0) return false;
  for (int i = 0; i < n; i++) {
    if (e->body_left) { e->body_left--; continue; }
    if (e->used + 1 >= sizeof e->header) return false;
    e->header[e->used++] = buf[i];
    if (e->used >= 4 && !memcmp(e->header + e->used - 4, "\r\n\r\n", 4)) {
      e->header[e->used] = 0;
      if (!handle_header(e)) return false;
      e->used = 0;
    }
  }
  return true;
}

void ap_events_poll(ap_conn *c, bool readable, uint32_t now_ms) {
  struct ap_events *e = c->events;
  bool ok = true;
  if (!e) return;
  if (e->fd < 0 && now_ms - e->last_send >= RETRY_MS) {
    e->fd = listen_port(&e->port);
    e->last_send = now_ms;
  }
  if (readable) {
    if (e->connected) ok = read_event(e);
    else accept_phone(c);
  }
  if (c == ap_mirror_owner && e->connected && !e->rejected && !e->tx_len && ok &&
      (__atomic_load_n(&frame_state, __ATOMIC_RELAXED) & (HAS_FRAME | NEED_IDR)) == (HAS_FRAME | NEED_IDR) &&
      (!e->attempted || now_ms - e->last_send >= RETRY_MS)) {
    e->last_send = now_ms;
    e->attempted = true;
    queue_keyframe_request(e);
  }
  if (e->connected && ok && e->tx_len) {
    int n = send(e->fd, e->tx, e->tx_len, MSG_DONTWAIT);
    if (n > 0) {
      if (e->keyframe_bytes) {
        if ((size_t)n >= e->keyframe_bytes) {
          e->keyframe_bytes = 0;
          __atomic_fetch_or(&frame_state, SENT_REQUEST, __ATOMIC_RELAXED);
          ap_log("mirror: requested keyframe (%u)", e->cseq);
        } else e->keyframe_bytes -= (size_t)n;
      }
      e->tx_len -= (size_t)n;
      memmove(e->tx, e->tx + n, e->tx_len);
    } else {
      ok = n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR);
    }
  }
  if (!ok) {
    close(e->fd);
    e->fd = listen_port(&e->port);
    e->last_send = now_ms;
    e->connected = false;
  }
  if (c == ap_mirror_owner)
    __atomic_store_n(&available, e->connected && !e->rejected, __ATOMIC_RELAXED);
}
