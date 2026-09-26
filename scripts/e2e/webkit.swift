// webkit: runs yip's browser journeys in the system WebKit (macOS), so they
// can be checked without downloading a browser. Nothing is persisted: the
// web view uses a non-persistent data store.
//
// Usage: webkit <base-url> <handle> <password> <journeys.js> <out-dir>
//
// journeys.js defines `window.__journeys = [{name, width, height, zoom?, path?, run: async (t) => {...}}]`.
// Each journey starts signed in at `path` (default /overview) and fails by
// throwing. `t` (see harness below) offers waiting, querying, typing, and
// real key presses delivered through AppKit (Tab, Enter, Escape, arrows,
// shortcuts), so focus movement and keyboard handling are the browser's own.
import AppKit
import WebKit

final class KeyWindow: NSWindow {
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { true }
}

let harness = #"""
window.__t = {
  sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
  async waitFor(fn, what, ms = 15000) {
    const t0 = Date.now();
    for (;;) {
      let v; try { v = fn(); } catch (e) { v = undefined; }
      if (v) return v;
      if (Date.now() - t0 > ms) throw new Error('timed out waiting for ' + what);
      await new Promise((r) => setTimeout(r, 50));
    }
  },
  q: (sel, root = document) => root.querySelector(sel),
  qa: (sel, root = document) => [...root.querySelectorAll(sel)],
  byText(sel, text, root = document) { return [...root.querySelectorAll(sel)].find((e) => (e.textContent || '').includes(text)); },
  text: () => document.body.innerText,
  expect(cond, msg) { if (!cond) throw new Error(msg); },
  overflowX: () => document.documentElement.scrollWidth - window.innerWidth,
  async press(key, mods = []) { await window.webkit.messageHandlers.native.postMessage({ press: key, mods }); await new Promise((r) => setTimeout(r, 60)); },
  async type(el, text) {
    el.focus();
    for (const ch of text) {
      const v = el.value + ch;
      const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, v);
      el.setSelectionRange?.(v.length, v.length);
      el.dispatchEvent(new InputEvent('input', { bubbles: true, data: ch, inputType: 'insertText' }));
      await new Promise((r) => setTimeout(r, 5));
    }
  },
  async click(el) { el.scrollIntoView({ block: 'center' }); el.click(); await new Promise((r) => setTimeout(r, 60)); },
  async goto(path) { history.pushState(null, '', path); dispatchEvent(new PopStateEvent('popstate')); await new Promise((r) => setTimeout(r, 300)); },
};
"""#

@MainActor
final class Runner: NSObject, WKNavigationDelegate, WKScriptMessageHandlerWithReply {
    let web: WKWebView
    let window: KeyWindow
    var pending: CheckedContinuation<Void, Never>?

    override init() {
        let cfg = WKWebViewConfiguration()
        cfg.websiteDataStore = .nonPersistent()
        // Like a keyboard user with "Press Tab to highlight each item" on:
        // Tab reaches links and buttons, not only form fields.
        cfg.preferences.tabFocusesLinks = true
        cfg.userContentController.addUserScript(WKUserScript(source: harness, injectionTime: .atDocumentEnd, forMainFrameOnly: true))
        web = WKWebView(frame: NSRect(x: 0, y: 0, width: 1440, height: 900), configuration: cfg)
        window = KeyWindow(contentRect: NSRect(x: -30000, y: -30000, width: 1440, height: 900), styleMask: [.borderless], backing: .buffered, defer: false)
        super.init()
        cfg.userContentController.addScriptMessageHandler(self, contentWorld: .page, name: "native")
        web.navigationDelegate = self
        window.contentView = web
        window.makeKeyAndOrderFront(nil)
        window.makeFirstResponder(web)
    }

    func load(_ url: URL) async {
        await withCheckedContinuation { c in
            pending = c
            web.load(URLRequest(url: url))
        }
    }

    nonisolated func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        MainActor.assumeIsolated { pending?.resume(); pending = nil }
    }

    nonisolated func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        MainActor.assumeIsolated { pending?.resume(); pending = nil }
    }

    nonisolated func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        MainActor.assumeIsolated { pending?.resume(); pending = nil }
    }

    // Real key presses: AppKit key events sent to the web view.
    nonisolated func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage,
                                           replyHandler: @escaping @MainActor @Sendable (Any?, String?) -> Void) {
        MainActor.assumeIsolated {
            let body = message.body as? [String: Any] ?? [:]
            let key = body["press"] as? String ?? ""
            let mods = body["mods"] as? [String] ?? []
            self.press(key, mods: mods)
            replyHandler(true, nil)
        }
    }

    func press(_ key: String, mods: [String]) {
        let table: [String: (String, UInt16)] = [
            "Tab": ("\t", 48), "Enter": ("\r", 36), "Escape": ("\u{1b}", 53),
            "ArrowDown": (String(UnicodeScalar(NSDownArrowFunctionKey)!), 125), "ArrowUp": (String(UnicodeScalar(NSUpArrowFunctionKey)!), 126),
            "ArrowLeft": (String(UnicodeScalar(NSLeftArrowFunctionKey)!), 123), "ArrowRight": (String(UnicodeScalar(NSRightArrowFunctionKey)!), 124),
            "Home": (String(UnicodeScalar(NSHomeFunctionKey)!), 115), "End": (String(UnicodeScalar(NSEndFunctionKey)!), 119), " ": (" ", 49),
            "k": ("k", 40), "Backspace": ("\u{7f}", 51),
        ]
        guard let (chars, code) = table[key] else { return }
        var flags: NSEvent.ModifierFlags = []
        if mods.contains("Shift") { flags.insert(.shift) }
        if mods.contains("Meta") { flags.insert(.command) }
        if mods.contains("Control") { flags.insert(.control) }
        window.makeKeyAndOrderFront(nil)
        if window.firstResponder !== web { window.makeFirstResponder(web) }
        for type in [NSEvent.EventType.keyDown, .keyUp] {
            guard let e = NSEvent.keyEvent(with: type, location: .zero, modifierFlags: flags, timestamp: ProcessInfo.processInfo.systemUptime,
                                           windowNumber: window.windowNumber, context: nil, characters: chars,
                                           charactersIgnoringModifiers: chars, isARepeat: false, keyCode: code) else { continue }
            if type == .keyDown {
                if flags.contains(.command), web.performKeyEquivalent(with: e) { continue }
                web.keyDown(with: e)
            } else {
                web.keyUp(with: e)
            }
        }
    }

    func resize(_ w: Double, _ h: Double) {
        window.setContentSize(NSSize(width: w, height: h))
        web.frame = NSRect(x: 0, y: 0, width: w, height: h)
    }

    func snapshot(_ path: String, width: Double) async {
        let conf = WKSnapshotConfiguration()
        conf.afterScreenUpdates = true
        conf.snapshotWidth = NSNumber(value: width)
        guard let image = try? await web.takeSnapshot(configuration: conf), let tiff = image.tiffRepresentation,
              let rep = NSBitmapImageRep(data: tiff), let png = rep.representation(using: .png, properties: [:]) else { return }
        try? png.write(to: URL(fileURLWithPath: path))
    }

    func run(base: String, handle: String, password: String, journeysJS: String, out: String) async -> Bool {
        guard let baseURL = URL(string: base) else { return false }
        try? FileManager.default.createDirectory(atPath: out, withIntermediateDirectories: true)
        await load(baseURL.appendingPathComponent("signin"))
        let status = try? await web.callAsyncJavaScript("""
            const r = await fetch('/v1/session', {method: 'POST', headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({handle, password})});
            return r.status;
            """, arguments: ["handle": handle, "password": password], contentWorld: .page)
        guard (status as? Int) == 200 else {
            print("sign-in failed:", status ?? "nil")
            return false
        }
        // List the journeys.
        await load(baseURL.appendingPathComponent("overview"))
        _ = try? await web.callAsyncJavaScript(journeysJS, arguments: [:], contentWorld: .page)
        let names = (try? await web.callAsyncJavaScript("return window.__journeys.map((j) => [j.name, j.width, j.height, j.zoom || 1, j.path || '/overview']);",
                                                        arguments: [:], contentWorld: .page)) as? [[Any]] ?? []
        var failed = 0
        for entry in names {
            guard entry.count == 5, let name = entry[0] as? String, let w = entry[1] as? Double, let h = entry[2] as? Double,
                  let zoom = entry[3] as? Double, let path = entry[4] as? String else { continue }
            resize(w, h)
            web.pageZoom = zoom
            await load(URL(string: base + path)!)
            // Inject the helpers directly too: a user script can lag a
            // navigation that finished early.
            _ = try? await web.callAsyncJavaScript(harness, arguments: [:], contentWorld: .page)
            _ = try? await web.callAsyncJavaScript(journeysJS, arguments: [:], contentWorld: .page)
            let started = Date()
            do {
                _ = try await web.callAsyncJavaScript("""
                    const j = window.__journeys.find((x) => x.name === name);
                    await window.__t.waitFor(() => document.querySelector('[data-screen-title], #room-title'), 'the app to load');
                    await j.run(window.__t);
                    return true;
                    """, arguments: ["name": name], contentWorld: .page)
                print(String(format: "  ok    %@ (%.1fs)", name, Date().timeIntervalSince(started)))
            } catch {
                failed += 1
                let msg = (error as NSError).userInfo["WKJavaScriptExceptionMessage"] as? String ?? "\(error)"
                print(String(format: "  FAIL  %@ (%.1fs): %@", name, Date().timeIntervalSince(started), msg))
            }
            let file = name.replacingOccurrences(of: "[^A-Za-z0-9]+", with: "-", options: .regularExpression)
            await snapshot((out as NSString).appendingPathComponent(file + ".png"), width: w)
        }
        print(failed == 0 ? "\(names.count) journeys passed" : "\(failed) of \(names.count) journeys failed")
        return failed == 0 && !names.isEmpty
    }
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let args = CommandLine.arguments
guard args.count == 6, let js = try? String(contentsOfFile: args[4], encoding: .utf8) else {
    FileHandle.standardError.write("usage: webkit <base-url> <handle> <password> <journeys.js> <out-dir>\n".data(using: .utf8)!)
    exit(2)
}
Task { @MainActor in
    let r = Runner()
    let ok = await r.run(base: args[1], handle: args[2], password: args[3], journeysJS: js, out: args[5])
    exit(ok ? 0 : 1)
}
app.run()
