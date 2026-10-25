import AppKit

// Persistent menu bar access and native URL delivery. The UI stays in the browser.
final class Launcher: NSObject, NSApplicationDelegate {
    private var receivedURL = false
    private var statusItem: NSStatusItem?
    private var openingPanel = false
    private var quitting = false
    private let processQueue = DispatchQueue(label: "org.webdevicecable.launcher")

    func applicationDidFinishLaunching(_ notification: Notification) {
        installStatusItem()
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
            if !self.receivedURL { self.openPanel() }
        }
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        receivedURL = true
        guard !quitting else { return }
        for url in urls {
            run(["launch", url.absoluteString]) { error in
                if let error = error, !self.quitting { self.showError(error) }
            }
        }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        openPanel()
        return false
    }

    private func installStatusItem() {
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        item.autosaveName = "WebDeviceCable"
        if let button = item.button {
            if let url = Bundle.main.url(forResource: "status-icon", withExtension: "svg"),
               let image = NSImage(contentsOf: url) {
                image.size = NSSize(width: 18, height: 18)
                image.isTemplate = true
                button.image = image
            } else {
                button.title = "WDC"
            }
            button.toolTip = "WebDeviceCable — click to open, right-click for options"
            button.setAccessibilityLabel("WebDeviceCable")
            button.target = self
            button.action = #selector(statusItemClicked(_:))
            button.sendAction(on: [.leftMouseUp, .rightMouseUp])
        }
        statusItem = item
    }

    @objc private func statusItemClicked(_ sender: NSStatusBarButton) {
        let event = NSApp.currentEvent
        if event?.type == .rightMouseUp || event?.modifierFlags.contains(.control) == true {
            let menu = NSMenu()
            let open = menu.addItem(withTitle: "Open WebDeviceCable", action: #selector(openPanel), keyEquivalent: "")
            open.target = self
            open.isEnabled = !quitting
            menu.addItem(.separator())
            let quit = menu.addItem(withTitle: "Quit WebDeviceCable", action: #selector(quit), keyEquivalent: "q")
            quit.target = self
            quit.isEnabled = !quitting
            menu.autoenablesItems = false
            menu.popUp(positioning: nil, at: NSPoint(x: 0, y: sender.bounds.minY), in: sender)
        } else {
            openPanel()
        }
    }

    @objc private func openPanel() {
        guard !quitting, !openingPanel else { return }
        openingPanel = true
        // `start` authenticates/reuses an existing runner or restarts it if stopped.
        run(["start"]) { error in
            self.openingPanel = false
            if let error = error, !self.quitting { self.showError(error) }
        }
    }

    @objc private func quit() {
        guard !quitting else { return }
        quitting = true
        run(["stop", "--if-running"]) { error in
            if let error = error {
                let alert = NSAlert()
                alert.messageText = "WebDeviceCable couldn’t stop"
                alert.informativeText = error + "\n\nQuitting the menu bar icon may leave the runner running."
                alert.addButton(withTitle: "Keep WDC Open")
                alert.addButton(withTitle: "Quit Menu Bar Icon")
                NSApp.activate(ignoringOtherApps: true)
                if alert.runModal() != .alertSecondButtonReturn {
                    self.quitting = false
                    return
                }
            }
            NSApp.terminate(nil)
        }
    }

    private func showError(_ detail: String) {
        let alert = NSAlert()
        alert.messageText = "WebDeviceCable couldn’t open"
        alert.informativeText = detail
        NSApp.activate(ignoringOtherApps: true)
        alert.runModal()
    }

    private func run(_ arguments: [String], completion: @escaping (String?) -> Void) {
        guard let executable = Bundle.main.url(forResource: "webdevicecable", withExtension: nil) else {
            completion("The bundled WebDeviceCable runner is missing. Reinstall the app and try again.")
            return
        }
        // Serialize starts and URL deliveries so rapid clicks cannot race startup.
        processQueue.async {
            let process = Process()
            process.executableURL = executable
            process.arguments = arguments
            process.standardInput = FileHandle.nullDevice
            process.standardOutput = FileHandle.nullDevice
            let errors = Pipe()
            process.standardError = errors
            do {
                try process.run()
                let data = errors.fileHandleForReading.readDataToEndOfFile()
                process.waitUntilExit()
                DispatchQueue.main.async {
                    let detail = String(decoding: data.prefix(2000), as: UTF8.self)
                        .trimmingCharacters(in: .whitespacesAndNewlines)
                    completion(process.terminationStatus == 0 ? nil :
                        (detail.isEmpty ? "Try opening WebDeviceCable again." : detail))
                }
            } catch {
                DispatchQueue.main.async { completion(error.localizedDescription) }
            }
        }
    }
}
let app = NSApplication.shared
let delegate = Launcher()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
