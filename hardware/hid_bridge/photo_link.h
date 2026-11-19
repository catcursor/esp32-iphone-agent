#pragma once
#include <Arduino.h>

// A camera-only credential provisioned through physical USB.
void photo_usb_pair();
bool photo_forget_key();
void photo_session(int socket, const char *boardNonceHex, const char *request);
