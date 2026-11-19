#include "sense_camera.h"
#include "logbuf.h"

#include <atomic>
#include <esp_camera.h>
#include <esp_heap_caps.h>
#include <freertos/FreeRTOS.h>
#include <freertos/task.h>

namespace {
std::atomic_flag capturing = ATOMIC_FLAG_INIT;

camera_config_t camera_config() {
  camera_config_t config{};
  // Seeed Studio XIAO ESP32S3 Sense camera expansion board.
  config.pin_pwdn = -1;
  config.pin_reset = -1;
  config.pin_xclk = 10;
  config.pin_sccb_sda = 40;
  config.pin_sccb_scl = 39;
  config.pin_d0 = 15;
  config.pin_d1 = 17;
  config.pin_d2 = 18;
  config.pin_d3 = 16;
  config.pin_d4 = 14;
  config.pin_d5 = 12;
  config.pin_d6 = 11;
  config.pin_d7 = 48;
  config.pin_vsync = 38;
  config.pin_href = 47;
  config.pin_pclk = 13;
  config.xclk_freq_hz = 10000000;
  config.ledc_timer = LEDC_TIMER_0;
  config.ledc_channel = LEDC_CHANNEL_0;
  config.pixel_format = PIXFORMAT_JPEG;
  config.frame_size = FRAMESIZE_VGA;
  config.jpeg_buffer_size = 192 * 1024;
  config.jpeg_quality = 15;
  config.fb_count = 1;
  config.fb_location = CAMERA_FB_IN_PSRAM;
  config.grab_mode = CAMERA_GRAB_WHEN_EMPTY;
  return config;
}

void log_heap(const char *stage) {
  const unsigned caps = MALLOC_CAP_INTERNAL | MALLOC_CAP_DMA;
  board_log("camera %s: DMA free=%u largest=%u PSRAM free=%u",
            stage, (unsigned)heap_caps_get_free_size(caps),
            (unsigned)heap_caps_get_largest_free_block(caps),
            (unsigned)heap_caps_get_free_size(MALLOC_CAP_SPIRAM));
}

camera_fb_t *get_frame(const char *stage) {
  const uint32_t start = millis();
  camera_fb_t *frame = esp_camera_fb_get();
  if (frame) {
    board_log("camera %s: %ux%u format=%d bytes=%u ms=%lu", stage,
              (unsigned)frame->width, (unsigned)frame->height, (int)frame->format,
              (unsigned)frame->len, (unsigned long)(millis() - start));
  } else {
    board_log("camera %s: frame timeout ms=%lu", stage, (unsigned long)(millis() - start));
  }
  return frame;
}

bool is_jpeg(const uint8_t *data, size_t len) {
  return data && len >= 4 && data[0] == 0xff && data[1] == 0xd8 &&
         data[len - 2] == 0xff && data[len - 1] == 0xd9;
}

esp_err_t deliver_frame(SenseCameraFrameHandler handler, void *context) {
  sensor_t *sensor = esp_camera_sensor_get();
  if (!sensor) return ESP_ERR_NOT_FOUND;
  const camera_sensor_info_t *info = esp_camera_sensor_get_info(&sensor->id);
  if (!info || !info->support_jpeg) return ESP_ERR_NOT_SUPPORTED;
  board_log("camera sensor=%s PID=0x%x xclk=%d", info->name,
            (unsigned)sensor->id.PID, sensor->xclk_freq_hz);
  if (!xTaskGetHandle("cam_task")) return ESP_ERR_NO_MEM;
  if (sensor->id.PID == OV3660_PID && sensor->set_vflip(sensor, 1) != 0)
    return ESP_FAIL;

  // Discard the startup frame after giving automatic exposure time to settle.
  vTaskDelay(pdMS_TO_TICKS(250));
  camera_fb_t *warmup = get_frame("warmup");
  if (!warmup) return ESP_ERR_TIMEOUT;
  esp_camera_fb_return(warmup);
  camera_fb_t *fb = get_frame("photo");
  if (!fb) return ESP_ERR_TIMEOUT;
  esp_err_t result = ESP_ERR_INVALID_RESPONSE;
  if (fb->format == PIXFORMAT_JPEG && is_jpeg(fb->buf, fb->len)) {
    const SenseCameraFrame frame{fb->buf, fb->len, fb->width, fb->height, info->name};
    result = handler(frame, context);
  }
  esp_camera_fb_return(fb);
  return result;
}
} // namespace

esp_err_t sense_camera_capture(SenseCameraFrameHandler handler, void *context) {
  if (!handler) return ESP_ERR_INVALID_ARG;
  if (capturing.test_and_set()) return ESP_ERR_INVALID_STATE;
  // AirPlay and BLE leave less contiguous internal RAM than the standard DMA buffer.
  if (!esp_camera_get_psram_mode()) esp_camera_set_psram_mode(true);
  const camera_config_t config = camera_config();
  log_heap("before init");
  esp_err_t result = esp_camera_init(&config);
  if (result == ESP_OK) {
    result = deliver_frame(handler, context);
    esp_err_t cleanup = esp_camera_deinit();
    if (result == ESP_OK) result = cleanup;
  }
  capturing.clear();
  return result;
}
