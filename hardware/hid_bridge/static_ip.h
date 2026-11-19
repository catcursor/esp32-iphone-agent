#pragma once
#include <Preferences.h>

void static_ip_prepare(Preferences &prefs, const String &ssid);
String static_ip_configure(Preferences &prefs, const String &ssid, const String &args, bool &changed);
void static_ip_connect(const String &ssid, const String &pass);
bool static_ip_waiting_for_probe();
bool static_ip_poll(Preferences &prefs);  // true when the receiver needs to advertise the new IP
