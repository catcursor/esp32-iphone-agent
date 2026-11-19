#pragma once
#include <Preferences.h>
#include <atomic>

// Each USB credential owns its NVS namespace. Clearing it also commits an already
// empty namespace. Keep the old key disabled in RAM if NVS reports a failure.
inline bool forgetUsbPairing(const char *name, std::atomic<bool> &revoked) {
  revoked.store(true);
  Preferences storage;
  return storage.begin(name, false) && storage.clear();
}
