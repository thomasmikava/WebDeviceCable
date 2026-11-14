/* WebDeviceCable website. Loaded in <head> without defer so the right download button
 * shows on first paint. It only sets attributes on <html>; CSS picks the matching button,
 * which the build already filled with real links. No requests, cookies or storage. */
(function (root) {
  'use strict';

  var DESKTOP = { darwin: true, windows: true, linux: true };
  var DEVICE_ONLY = { android: true, ios: true, ipados: true, chromeos: true };

  // Pure: browser facts in, best guess out. `hints` is undefined while the browser's
  // high-entropy client hints are still loading, and null when they are unavailable.
  function detectPlatform(env) {
    env = env || {};
    var ua = String(env.userAgent || '');
    var platform = String(env.platform || '');
    var touch = Number(env.maxTouchPoints) || 0;
    var uaData = env.uaData || null;
    var chPlatform = uaData && typeof uaData.platform === 'string' ? uaData.platform.toLowerCase() : '';
    var hints = env.hints;
    var looksLikeMac = /macintosh|mac os x/i.test(ua) || /^mac/i.test(platform);
    var os = 'unknown';

    if (/bot|crawler|spider|slurp/i.test(ua)) os = 'unknown';
    else if (/android/i.test(ua) || chPlatform === 'android') os = 'android';
    else if (/iphone|ipod/i.test(ua) || chPlatform === 'ios') os = 'ios';
    else if (/ipad/i.test(ua) || (looksLikeMac && touch > 1)) os = 'ipados';
    else if (/\bCrOS\b/.test(ua) || chPlatform === 'chrome os' || chPlatform === 'chromeos') os = 'chromeos';
    else if (chPlatform === 'windows' || /windows nt/i.test(ua) || /^win/i.test(platform)) os = 'windows';
    else if (chPlatform === 'macos' || looksLikeMac) os = 'darwin';
    else if (chPlatform === 'linux' || /linux/i.test(ua) || /linux/i.test(platform)) os = 'linux';

    if (!DESKTOP[os]) return { os: os, arch: 'unknown', confidence: 'none', target: null };

    // Apple Silicon is the safer Mac default: Safari and Firefox report every Mac as Intel.
    var fallback = os === 'darwin' ? 'arm64' : 'amd64';
    var arch = fallback;
    var confidence = 'low';
    var hintArch = hints && hints.architecture ? String(hints.architecture).toLowerCase() : '';
    var bits32 = hints && hints.bitness === '32';

    if (hintArch === 'arm') {
      arch = bits32 ? 'unknown' : 'arm64';
      confidence = bits32 ? 'none' : 'high';
    } else if (hintArch === 'x86') {
      arch = 'amd64';
      confidence = bits32 ? 'low' : 'high';
    } else if (uaData && hints === undefined) {
      confidence = 'pending';
    } else if (!uaData || hints === null || !hintArch) {
      var text = ua + ' ' + platform;
      if (os === 'windows' && /arm64|aarch64/i.test(text)) { arch = 'arm64'; confidence = 'high'; }
      if (os === 'linux') {
        if (/aarch64|arm64|armv8/i.test(text)) { arch = 'arm64'; confidence = 'high'; }
        else if (/x86_64|amd64/i.test(text)) { arch = 'amd64'; confidence = 'high'; }
        else if (/i[3-6]86|armv[67]/i.test(text)) { arch = 'unknown'; confidence = 'none'; }
      }
    }

    return { os: os, arch: arch, confidence: confidence, target: arch === 'unknown' ? null : os + '/' + arch };
  }

  function apply(doc, result) {
    var el = doc.documentElement;
    var key = result.target ? result.target.replace('/', '-') : (DEVICE_ONLY[result.os] ? 'unsupported' : 'default');
    el.setAttribute('data-os', result.os);
    el.setAttribute('data-target', key);
    el.setAttribute('data-arch-confidence', result.confidence);
  }

  function init(win) {
    var doc = win.document;
    var nav = win.navigator || {};
    var data = nav.userAgentData;
    var env = {
      userAgent: nav.userAgent,
      platform: nav.platform,
      maxTouchPoints: nav.maxTouchPoints,
      uaData: data ? { platform: data.platform, mobile: data.mobile } : null,
      hints: data ? undefined : null,
    };
    apply(doc, detectPlatform(env));

    var done = function (hints) {
      env.hints = hints && typeof hints === 'object' ? hints : null;
      apply(doc, detectPlatform(env));
    };
    if (data && typeof data.getHighEntropyValues === 'function') {
      try {
        data.getHighEntropyValues(['architecture', 'bitness']).then(done, function () { done(null); });
      } catch (error) {
        done(null);
      }
    } else if (data) {
      done(null);
    }

    // After a download starts, show how to install it. Scrolling never navigates,
    // so the download is not interrupted.
    doc.addEventListener('click', function (event) {
      var link = event.target && event.target.closest ? event.target.closest('a[data-download]') : null;
      if (!link || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      win.setTimeout(function () {
        var install = doc.getElementById('install');
        if (install) install.scrollIntoView({ block: 'start' });
      }, 200);
    });
  }

  root.WDCSite = { detectPlatform: detectPlatform, apply: apply, init: init };
  if (root.document && root.navigator) init(root);
})(typeof window !== 'undefined' ? window : globalThis);
