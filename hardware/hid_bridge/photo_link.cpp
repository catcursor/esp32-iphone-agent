#include "photo_link.h"
#include "cli_link.h"
#include "sense_camera.h"
#include "usb_pairing.h"
#include "src/cli/cli_proto.h"
#include "src/net/lan_lock.h"
#include "logbuf.h"
#include <algorithm>
#include <esp_random.h>
#include <lwip/sockets.h>
#include <mbedtls/platform_util.h>

namespace {
constexpr size_t Chunk = 512;
constexpr char PhotoNamespace[] = "sense-photo";
constexpr char PhotoKey[] = "key";
std::atomic<bool> legacyKeyRevoked{false};
struct Transfer {
  int fd;
  uint8_t key[16];
  uint32_t counter = 1;
  bool headerSent = false;
};

bool loadKey(uint8_t key[CLI_KEY_LEN], bool create) {
  const bool revoked = legacyKeyRevoked.load();
  if (revoked && !create) return false;
  Preferences prefs;
  if (!prefs.begin(PhotoNamespace, !create)) return false;
  if (!revoked && prefs.getBytesLength(PhotoKey) == CLI_KEY_LEN)
    return prefs.getBytes(PhotoKey, key, CLI_KEY_LEN) == CLI_KEY_LEN && !legacyKeyRevoked.load();
  if (!create) return false;
  esp_fill_random(key, CLI_KEY_LEN);
  if (prefs.putBytes(PhotoKey, key, CLI_KEY_LEN) != CLI_KEY_LEN) return false;
  legacyKeyRevoked.store(false);
  return true;
}

int nibble(char c) {
  if (c >= '0' && c <= '9') return c - '0';
  if (c >= 'a' && c <= 'f') return c - 'a' + 10;
  return -1;
}

bool decode16(const char *hex, uint8_t out[16]) {
  if (strlen(hex) != 32) return false;
  for (size_t i = 0; i < 16; ++i) {
    int high = nibble(hex[i * 2]), low = nibble(hex[i * 2 + 1]);
    if (high < 0 || low < 0) return false;
    out[i] = high * 16 + low;
  }
  return true;
}

bool writeAll(int fd, const void *data, size_t size) {
  const uint8_t *p = static_cast<const uint8_t *>(data);
  while (size) {
    int n = send(fd, p, size, 0);
    if (n <= 0) return false;
    p += n;
    size -= n;
  }
  return true;
}

bool record(Transfer &t, uint8_t type, const uint8_t *data, size_t n) {
  // Stage the camera's PSRAM data in internal RAM for the hardware AES DMA.
  uint8_t plain[Chunk], wire[2 + CLI_OVERHEAD + Chunk];
  if (n) memcpy(plain, data, n);
  const size_t size = cli_seal(t.key, type, 'P', t.counter++, plain, n, wire + 2);
  if (!size) return false;
  wire[0] = size & 0xff;
  wire[1] = size >> 8;
  return writeAll(t.fd, wire, size + 2);
}

esp_err_t sendPhoto(const SenseCameraFrame &frame, void *ctx) {
  Transfer &t = *static_cast<Transfer *>(ctx);
  t.headerSent = true;
  if (!writeAll(t.fd, "ok photo\n", 9)) return ESP_FAIL;
  for (size_t off = 0; off < frame.size; off += Chunk) {
    size_t n = std::min(Chunk, frame.size - off);
    if (!record(t, 'J', frame.data + off, n)) return ESP_FAIL;
  }
  if (!record(t, 'E', nullptr, 0)) return ESP_FAIL;
  board_log("camera: %s %ux%u, %u bytes", frame.sensor, unsigned(frame.width),
            unsigned(frame.height), unsigned(frame.size));
  return ESP_OK;
}

bool tryPhotoKey(const uint8_t *key, const uint8_t *client, const uint8_t *board,
                 const uint8_t *proof, uint8_t txKey[16]) {
  uint8_t expected[CLI_MAC_LEN], unused[16];
  cli_mac(key, "photo", client, CLI_NONCE_LEN, board, CLI_NONCE_LEN, expected);
  unsigned diff = 0;
  for (size_t i = 0; i < sizeof expected; ++i) diff |= proof[i] ^ expected[i];
  const bool ok = !diff && cli_session(key, client, board, unused, txKey);
  mbedtls_platform_zeroize(unused, sizeof unused);
  return ok;
}

bool authenticate(const char *nonce, const char *request, uint8_t txKey[16]) {
  char clientHex[33], proofHex[33], extra;
  uint8_t client[CLI_NONCE_LEN], board[CLI_NONCE_LEN], proof[CLI_MAC_LEN], key[CLI_KEY_LEN];
  if (sscanf(request, "photo %32s %32s %c", clientHex, proofHex, &extra) != 2 ||
      !decode16(nonce, board) || !decode16(clientHex, client) || !decode16(proofHex, proof)) return false;
  // Only this endpoint accepts the original camera-only credential.
  bool ok = cli_owner_photo_key(key) && tryPhotoKey(key, client, board, proof, txKey);
  if (!ok) ok = loadKey(key, false) && tryPhotoKey(key, client, board, proof, txKey);
  mbedtls_platform_zeroize(key, sizeof key);
  return ok;
}
} // namespace

bool photo_forget_key() {
  return forgetUsbPairing(PhotoNamespace, legacyKeyRevoked);
}

void photo_usb_pair() {
  uint8_t key[CLI_KEY_LEN];
  if (!loadKey(key, true)) {
    mbedtls_platform_zeroize(key, sizeof key);
    Serial.println("0 err camera key unavailable");
    return;
  }
  char reply[67] = "0 ";
  lan_nonce(reply + 2, key);
  lan_nonce(reply + 34, key + 16);
  mbedtls_platform_zeroize(key, sizeof key);
  Serial.println(reply);
  mbedtls_platform_zeroize(reply, sizeof reply);
}

void photo_session(int fd, const char *nonce, const char *request) {
  Transfer t{};
  t.fd = fd;
  if (!authenticate(nonce, request, t.key)) {
    writeAll(fd, "err camera authentication\n", 26);
    return;
  }
  timeval timeout{4, 0};
  setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof timeout);
  const esp_err_t result = sense_camera_capture(sendPhoto, &t);
  mbedtls_platform_zeroize(t.key, sizeof t.key);
  if (result != ESP_OK && !t.headerSent) {
    char message[96];
    snprintf(message, sizeof message, "err camera %s\n", esp_err_to_name(result));
    writeAll(fd, message, strlen(message));
  }
}
