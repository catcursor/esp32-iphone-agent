// Exercise the negotiated reverse RTSP socket with a fake mirroring source on loopback.
#include <arpa/inet.h>
#include <assert.h>
#include <errno.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>
#include "../hid_bridge/src/airplay/ap_internal.h"
#include "../hid_bridge/src/bplist/bplist.h"

ap_conn *ap_mirror_owner;

static int dial(uint16_t port) {
  int fd = socket(AF_INET, SOCK_STREAM, 0);
  assert(fd >= 0);
  struct timeval tv = {2, 0};
  setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv);
  struct sockaddr_in addr = {.sin_family = AF_INET, .sin_port = htons(port), .sin_addr.s_addr = htonl(INADDR_LOOPBACK)};
  assert(connect(fd, (struct sockaddr *)&addr, sizeof addr) == 0);
  return fd;
}

static void no_data(int fd) {
  char c;
  assert(recv(fd, &c, 1, MSG_DONTWAIT) < 0 && (errno == EAGAIN || errno == EWOULDBLOCK));
}

static void read_command(int fd, unsigned seq) {
  char head[256] = {0};
  size_t used = 0;
  while (!strstr(head, "\r\n\r\n")) {
    assert(used + 1 < sizeof head && recv(fd, head + used, 1, 0) == 1);
    used++;
  }
  char expected[96];
  snprintf(expected, sizeof expected, "POST /command RTSP/1.0\r\nCSeq: %u\r\n", seq);
  assert(!strncmp(head, expected, strlen(expected)) && strstr(head, "application/x-apple-binary-plist"));
  char *length = strstr(head, "Content-Length:");
  assert(length);
  int size = atoi(length + strlen("Content-Length:"));
  uint8_t body[256];
  assert(size > 0 && size <= (int)sizeof body && recv(fd, body, size, MSG_WAITALL) == size);
  bp_reader r;
  int top = bp_open(&r, body, size);
  char command[32];
  assert(bp_get_string(&r, bp_dict_get(&r, top, "type"), command, sizeof command));
  assert(!strcmp(command, "forceKeyFrame"));
}

void test_event_channel(void) {
  ap_conn c = {.fd = -1, .remote_ip = htonl(0x7f000002)};
  ap_mirror_owner = &c;
  uint16_t port = ap_events_open(&c);
  assert(port && port == ap_events_open(&c));
  int other = dial(port);
  ap_events_poll(&c, true, 1);
  char ch;
  assert(recv(other, &ch, 1, 0) == 0 && !airplay_keyframe_supported());
  close(other);

  c.remote_ip = htonl(INADDR_LOOPBACK);
  int phone = dial(port);
  airplay_request_keyframe();
  ap_events_poll(&c, true, 2);
  assert(airplay_keyframe_supported());
  no_data(phone);  // wait for a picture before requesting another
  ap_events_frame(false);
  ap_events_poll(&c, false, 3);
  read_command(phone, 1);

  // An acknowledged request must remain pending until an independent frame.
  const char *part1 = "RTSP/1.0 200 OK\r\nCSeq: 1\r\nContent-Length: 3\r\n";
  const char *part2 = "\r\nabcPOST /feedback RTSP/1.0\r\nCSeq: 25\r\n\r\n";
  assert(send(phone, part1, strlen(part1), 0) == (int)strlen(part1));
  ap_events_poll(&c, true, 100);
  assert(send(phone, part2, strlen(part2), 0) == (int)strlen(part2));
  ap_events_poll(&c, true, 300);
  const char *ack = "RTSP/1.0 200 OK\r\nCSeq: 25\r\nContent-Length: 0\r\n\r\n";
  char reply[96];
  assert(recv(phone, reply, strlen(ack), MSG_WAITALL) == (int)strlen(ack) && !memcmp(reply, ack, strlen(ack)));
  ap_events_poll(&c, false, 5002);
  no_data(phone);
  ap_events_poll(&c, false, 5003);
  read_command(phone, 2);
  ap_events_frame(true);
  ap_events_poll(&c, false, 11000);
  no_data(phone);

  close(phone);
  ap_events_poll(&c, true, 12000);
  assert(!airplay_keyframe_supported());
  phone = dial(port);
  ap_events_poll(&c, true, 12001);
  assert(airplay_keyframe_supported());
  airplay_request_keyframe();
  ap_events_poll(&c, false, 12002);
  read_command(phone, 3);
  const char *rejected = "RTSP/1.0 400 Bad Request\r\nCSeq: 3\r\n\r\n";
  assert(send(phone, rejected, strlen(rejected), 0) == (int)strlen(rejected));
  ap_events_poll(&c, true, 12100);
  ap_events_poll(&c, false, 22000);
  assert(!airplay_keyframe_supported());
  no_data(phone);
  ap_events_close(&c);
  assert(!airplay_keyframe_supported() && ap_events_fd(&c) < 0);
  assert(recv(phone, &ch, 1, 0) == 0);
  close(phone);
  ap_mirror_owner = NULL;
}
