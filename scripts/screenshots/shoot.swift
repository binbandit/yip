// shoot: captures screenshots of a yip hub with the system WebKit.
// Usage: shoot config.json
// Nothing is persisted: the web view uses a non-persistent data store.
import AppKit
import WebKit

struct Shot: Decodable {
    let name: String
    let path: String
    let width: Double
    let height: Double
    let dark: Bool?
    let wait: Double?
    let js: String?
    let scale: Double?
}

struct Config: Decodable {
    let base: String
    let handle: String
    let password: String
    let out: String
    let shots: [Shot]
}

@MainActor
final class Shooter: NSObject, WKNavigationDelegate {
    let web: WKWebView
    let window: NSWindow
    var pending: CheckedContinuation<Void, Never>?

    override init() {
        let cfg = WKWebViewConfiguration()
        cfg.websiteDataStore = .nonPersistent()
        // Offscreen windows throttle animations; show every element in its
        // final state instead of mid-transition.
        let css = "*, *::before, *::after { animation-duration: 0s !important; animation-delay: 0s !important; transition-duration: 0s !important; transition-delay: 0s !important; }"
        let script = WKUserScript(source: "const st = document.createElement('style'); st.textContent = \"\(css)\"; document.documentElement.appendChild(st);",
                                  injectionTime: .atDocumentEnd, forMainFrameOnly: true)
        cfg.userContentController.addUserScript(script)
        web = WKWebView(frame: NSRect(x: 0, y: 0, width: 1440, height: 900), configuration: cfg)
        window = NSWindow(contentRect: NSRect(x: -30000, y: -30000, width: 1440, height: 900),
                          styleMask: [.borderless], backing: .buffered, defer: false)
        super.init()
        web.navigationDelegate = self
        window.contentView = web
        window.orderFrontRegardless()
    }

    func load(_ url: URL) async {
        await withCheckedContinuation { c in
            pending = c
            web.load(URLRequest(url: url))
        }
    }

    private func finish() {
        pending?.resume()
        pending = nil
    }

    nonisolated func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        MainActor.assumeIsolated { finish() }
    }

    nonisolated func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        MainActor.assumeIsolated {
            FileHandle.standardError.write("navigation failed: \(error)\n".data(using: .utf8)!)
            finish()
        }
    }

    nonisolated func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        MainActor.assumeIsolated {
            FileHandle.standardError.write("navigation failed: \(error)\n".data(using: .utf8)!)
            finish()
        }
    }

    func resize(_ w: Double, _ h: Double) {
        window.setContentSize(NSSize(width: w, height: h))
        web.frame = NSRect(x: 0, y: 0, width: w, height: h)
    }

    func pause(_ seconds: Double) async {
        try? await Task.sleep(nanoseconds: UInt64(seconds * 1_000_000_000))
    }

    func run(_ cfg: Config) async throws {
        guard let base = URL(string: cfg.base) else { throw NSError(domain: "shoot", code: 1) }
        await load(base.appendingPathComponent("signin"))
        let status = try await web.callAsyncJavaScript("""
            const r = await fetch('/v1/session', {method: 'POST', headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({handle: handle, password: password})});
            return r.status;
            """, arguments: ["handle": cfg.handle, "password": cfg.password], contentWorld: .page)
        print("sign-in status:", status ?? "nil")
        try FileManager.default.createDirectory(atPath: cfg.out, withIntermediateDirectories: true)
        for s in cfg.shots {
            resize(s.width, s.height)
            web.appearance = NSAppearance(named: (s.dark ?? false) ? .darkAqua : .aqua)
            guard let url = URL(string: cfg.base + s.path) else { continue }
            await load(url)
            await pause(s.wait ?? 2.5)
            if let js = s.js {
                _ = try? await web.callAsyncJavaScript(js, arguments: [:], contentWorld: .page)
                await pause(1.2)
            }
            let conf = WKSnapshotConfiguration()
            conf.afterScreenUpdates = true
            // Render at 2x so text is sharp on high-density displays.
            conf.snapshotWidth = NSNumber(value: s.width * (s.scale ?? 2))
            let image = try await web.takeSnapshot(configuration: conf)
            guard let tiff = image.tiffRepresentation, let rep = NSBitmapImageRep(data: tiff),
                  let png = rep.representation(using: .png, properties: [:]) else {
                throw NSError(domain: "shoot", code: 2)
            }
            let path = (cfg.out as NSString).appendingPathComponent(s.name + ".png")
            try png.write(to: URL(fileURLWithPath: path))
            print("wrote", path, rep.pixelsWide, "x", rep.pixelsHigh)
        }
    }
}

let app = NSApplication.shared
app.setActivationPolicy(.prohibited)
let args = CommandLine.arguments
guard args.count == 2, let data = FileManager.default.contents(atPath: args[1]),
      let cfg = try? JSONDecoder().decode(Config.self, from: data) else {
    FileHandle.standardError.write("usage: shoot config.json\n".data(using: .utf8)!)
    exit(2)
}
Task { @MainActor in
    let s = Shooter()
    do {
        try await s.run(cfg)
        exit(0)
    } catch {
        FileHandle.standardError.write("error: \(error)\n".data(using: .utf8)!)
        exit(1)
    }
}
app.run()
