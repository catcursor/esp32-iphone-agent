#include "static_ip.h"
#include "logbuf.h"
#include <WiFi.h>
#include <esp_netif.h>
#include <esp_wifi.h>
#include <lwip/etharp.h>
#include <lwip/netif.h>
#include "src/net/lan_lock.h"

namespace {
IPAddress address;
constexpr char profileKey[] = "sta_ipv4_v1";

struct Route {
  uint32_t gateway, mask, dns1, dns2;
};
struct Profile {
  char ssid[33];
  uint32_t target;
  Route route;
};
Profile profile{};
Route route{};
bool enabled, savedRoute, probing;
unsigned probesSent;
uint32_t nextProbe;

bool usableRoute(uint32_t target, uint32_t local, const Route &r) {
  const uint32_t hostMask = ~ntohl(r.mask);
  return r.mask && !(hostMask & (hostMask + 1)) &&
         r.gateway && r.gateway != target && ((local ^ target) & r.mask) == 0 &&
         ((r.gateway ^ target) & r.mask) == 0 && (target & ~r.mask) && (~target & ~r.mask);
}

void keepDhcp(const char *reason) {
  enabled = false;
  probing = false;
  board_log("net: %s; using DHCP", reason);
  // On a cached route the interface is associated but has no IPv4 address yet.
  if (savedRoute && !WiFi.STA.config()) board_log("net: failed to restart DHCP");
}

// Keep the unnumbered interface on the air while looking for a conflicting owner.
// esp-netif starts DHCP on association unless we stop it *before* esp_wifi_connect.
bool associateWithoutAddress(const String &ssid, const String &pass) {
  if (!WiFi.STA.begin() || !WiFi.STA.connect(ssid.c_str(), pass.c_str(), 0, nullptr, false))
    return false;
  esp_netif_t *sta = WiFi.STA.netif();
  esp_err_t err = esp_netif_dhcpc_stop(sta);
  if (err != ESP_OK && err != ESP_ERR_ESP_NETIF_DHCP_ALREADY_STOPPED) return false;
  esp_netif_ip_info_t empty{};
  return esp_netif_set_ip_info(sta, &empty) == ESP_OK && esp_wifi_connect() == ESP_OK;
}

struct Probe {
  int index;
  bool send, occupied, gatewaySeen;
};

esp_err_t checkArp(void *arg) {
  auto &probe = *static_cast<Probe *>(arg);
  netif *station = netif_get_by_index(probe.index);
  if (!station || !netif_is_up(station) || !netif_is_link_up(station)) return ESP_FAIL;
  ip4_addr_t target{static_cast<uint32_t>(address)}, gateway{route.gateway};
  eth_addr *mac;
  const ip4_addr_t *ip;
  probe.occupied = etharp_find_addr(station, &target, &mac, &ip) >= 0;
  probe.gatewaySeen = etharp_find_addr(station, &gateway, &mac, &ip) >= 0;
  if (probe.send && !probe.occupied) {
    if (etharp_query(station, &target, nullptr) != ERR_OK) return ESP_FAIL;
    if (!probe.gatewaySeen && etharp_query(station, &gateway, nullptr) != ERR_OK) return ESP_FAIL;
  }
  return ESP_OK;
}
}  // namespace

void static_ip_prepare(Preferences &prefs, const String &ssid) {
  profile = {};
  if (prefs.getBytesLength(profileKey) == sizeof(profile))
    prefs.getBytes(profileKey, &profile, sizeof(profile));
  profile.ssid[sizeof(profile.ssid) - 1] = 0;
  enabled = profile.target && ssid == profile.ssid;
  address = IPAddress(profile.target);
  route = profile.route;
  probing = false;
  savedRoute = enabled && usableRoute(address, address, route);
}

String static_ip_policy(const String &ssid) {
  if (profile.target && ssid == profile.ssid)
    return String("ok {\"policy\":\"static\",\"address\":\"") + IPAddress(profile.target).toString() + "\"}";
  return "ok {\"policy\":\"dhcp\",\"address\":\"\"}";
}

String static_ip_configure(Preferences &prefs, const String &ssid, const String &args, bool &changed) {
  changed = false;
  if (args.length() == 0) return static_ip_policy(ssid);
  if (ssid.isEmpty() || ssid.length() > 32) return "err select a station Wi-Fi network first";
  Profile next{};
  if (args == "dhcp") {
    if (ssid != profile.ssid || !profile.target) return static_ip_policy(ssid);
    if (!prefs.remove(profileKey)) return "err cannot save DHCP policy";
  } else {
    uint32_t target = 0;
    if (!args.startsWith("static ") || !lan_parse_ip(args.c_str() + 7, &target))
      return "err use ipv4 static <IPv4 address> or ipv4 dhcp";
    const uint8_t first = ntohl(target) >> 24;
    if (!first || first == 127 || first >= 224) return "err expected a unicast IPv4 address";
    const bool connected = WiFi.status() == WL_CONNECTED && static_cast<uint32_t>(WiFi.localIP());
    if (ssid == profile.ssid && profile.target == target && connected && WiFi.localIP() == IPAddress(target))
      return static_ip_policy(ssid);
    strlcpy(next.ssid, ssid.c_str(), sizeof(next.ssid));
    next.target = target;
    if (connected) {
      next.route = {WiFi.gatewayIP(), WiFi.subnetMask(), WiFi.dnsIP(0), WiFi.dnsIP(1)};
      if (!usableRoute(target, WiFi.localIP(), next.route)) return "err address is not a host on the connected subnet";
    }
    if (prefs.putBytes(profileKey, &next, sizeof(next)) != sizeof(next)) return "err cannot save static IP";
  }
  profile = next;
  probing = false;
  changed = true;
  return static_ip_policy(ssid);
}

void static_ip_connect(const String &ssid, const String &pass) {
  probing = enabled;
  probesSent = 0;
  nextProbe = millis();
  if (enabled && savedRoute) {
    if (associateWithoutAddress(ssid, pass)) return;
    keepDhcp("could not start address probing");
  }
  WiFi.begin(ssid.c_str(), pass.c_str());
}

bool static_ip_waiting_for_probe() {
  return probing && savedRoute && WiFi.STA.connected();
}

bool static_ip_poll(Preferences &prefs) {
  if (!probing || !WiFi.STA.connected() || (int32_t)(millis() - nextProbe) < 0) return false;
  if (!savedRoute) {
    if (WiFi.status() != WL_CONNECTED || !static_cast<uint32_t>(WiFi.localIP())) return false;
    route = {WiFi.gatewayIP(), WiFi.subnetMask(), WiFi.dnsIP(0), WiFi.dnsIP(1)};
    if (!usableRoute(address, WiFi.localIP(), route)) {
      keepDhcp("requested IP is outside the DHCP subnet or is reserved");
      return false;
    }
  }

  Probe probe{esp_netif_get_netif_impl_index(WiFi.STA.netif()), probesSent < 3, false, false};
  if (probe.index <= 0 || esp_netif_tcpip_exec(checkArp, &probe) != ESP_OK) {
    keepDhcp("could not probe the local link");
    return false;
  }
  if (probe.occupied) {
    keepDhcp("requested address already responds to ARP");
    return false;
  }
  if (probe.send) {
    ++probesSent;
    nextProbe = millis() + 1000;
    return false;
  }
  if (!probe.gatewaySeen) {
    if (savedRoute && WiFi.STA.config()) {
      // A router can be replaced without changing the SSID. Learn its new route once.
      profile.route = {};
      prefs.putBytes(profileKey, &profile, sizeof(profile));
      savedRoute = false;
      probesSent = 0;
      nextProbe = millis() + 1000;
      board_log("net: saved gateway unavailable; learning route again");
      return false;
    }
    keepDhcp("gateway did not answer ARP");
    return false;
  }
  probing = false;
  if (!WiFi.STA.config(address, IPAddress(route.gateway), IPAddress(route.mask),
                       IPAddress(route.dns1), IPAddress(route.dns2))) {
    enabled = false;
    WiFi.STA.config();  // a partially applied static configuration may already have stopped DHCP
    board_log("net: could not apply static address; using DHCP");
    return false;
  }
  if (!savedRoute) {
    profile.route = route;
    if (prefs.putBytes(profileKey, &profile, sizeof(profile)) != sizeof(profile))
      board_log("net: could not save route; will learn it again next boot");
  }
  savedRoute = true;
  board_log("net: static IP %s", address.toString().c_str());
  return true;
}
