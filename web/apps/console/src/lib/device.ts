import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/** The browser and the operating system a User-Agent names, as far as it says. */
export type Device = { browser?: string; os?: string };

/* eslint-disable lingui/no-unlocalized-strings -- product names, written the same in every language */
// Order matters: Edge and Opera say Chrome too, Chrome says Safari too.
const browsers: [RegExp, string][] = [
  [/\bEdg(?:e|A|iOS)?\//, "Edge"],
  [/\b(?:OPR|Opera)\//, "Opera"],
  [/\b(?:Firefox|FxiOS)\//, "Firefox"],
  [/\b(?:Headless)?(?:Chrome|CriOS|Chromium)\//, "Chrome"],
  [/\bVersion\/[\d.]+.*\bSafari\//, "Safari"],
];

const systems: [RegExp, string][] = [
  [/\b(?:iPhone|iPad|iPod)\b/, "iOS"],
  [/\bAndroid\b/, "Android"],
  [/\bCrOS\b/, "ChromeOS"],
  [/\bWindows\b/, "Windows"],
  [/\bMac OS X\b|\bMacintosh\b/, "macOS"],
  [/\bLinux\b/, "Linux"],
];
/* eslint-enable lingui/no-unlocalized-strings */

/** The browser and system of a User-Agent; names are products, not translated. */
export function deviceOf(userAgent: string): Device {
  const browser = browsers.find(([pattern]) => pattern.test(userAgent))?.[1];
  const os = systems.find(([pattern]) => pattern.test(userAgent))?.[1];
  return { browser, os };
}

/**
 * What a member recognises a device by: "Chrome on macOS", the browser or
 * the system alone, else the start of the User-Agent itself, such as a
 * script's client name.
 */
export function deviceName(i18n: I18n, userAgent: string): string {
  const { browser, os } = deviceOf(userAgent);
  if (browser && os) return i18n._(msg`${browser} on ${os}`);
  if (browser ?? os) return (browser ?? os)!;
  const raw = userAgent.trim().split(/\s+/)[0];
  return raw ? raw.slice(0, 40) : i18n._(msg`Unknown device`);
}
