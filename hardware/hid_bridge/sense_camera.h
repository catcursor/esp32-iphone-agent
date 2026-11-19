#pragma once

#include <stddef.h>
#include <stdint.h>
#include <esp_err.h>

struct SenseCameraFrame {
  const uint8_t *data;
  size_t size;
  size_t width;
  size_t height;
  const char *sensor;
};

// The JPEG is valid for the duration of this synchronous callback.
using SenseCameraFrameHandler = esp_err_t (*)(const SenseCameraFrame &frame, void *context);

// Capture one JPEG on the XIAO ESP32S3 Sense camera attachment. Concurrent
// requests return ESP_ERR_INVALID_STATE. A handler error propagates to the caller.
esp_err_t sense_camera_capture(SenseCameraFrameHandler handler, void *context);
