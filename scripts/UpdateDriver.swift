import AppKit
#if canImport(Sparkle)
import Sparkle

// Sparkle owns macOS installation and OS authorization. The same web Settings
// card owns user choices; the runner owns the final no-active-work barrier.
final class UpdateDriver: NSObject, SPUUserDriver, SPUUpdaterDelegate {
    typealias Execute = ([String], @escaping (Data?, String?) -> Void) -> Void
    private let execute: Execute
    private var updater: SPUUpdater!
    private var timer: Timer?
    private var polling = false
    private var expected = ""
    private var expectedURL = ""
    private var expectedSize: UInt64 = 0
    private var ready: ((SPUUserUpdateChoice) -> Void)?
    private var onQuit: (() -> Void)?
    private var authorizedInstall = false
    private var downloadedChoice: ((SPUUserUpdateChoice) -> Void)?

    init(execute: @escaping Execute) {
        self.execute = execute
        super.init()
        updater = SPUUpdater(hostBundle: .main, applicationBundle: .main, userDriver: self, delegate: self)
        updater.automaticallyChecksForUpdates = false
        updater.automaticallyDownloadsUpdates = false
        do { try updater.start() } catch { return }
        timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in self?.poll() }
    }
    private func report(_ stage: String) {
        execute(["update", "native-report", "--version", expected, "--stage", stage]) { _, _ in }
    }
    private func poll() {
        guard !polling else { return }; polling = true
        execute(["update", "native-poll"]) { [weak self] data, _ in
            guard let self = self else { return }; self.polling = false
            guard let data = data, let command = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] else { return }
            let action = command["action"] as? String ?? ""
            if action == "install", !self.authorizedInstall,
               let version = command["version"] as? String,
               let url = command["url"] as? String,
               let size = command["size"] as? NSNumber {
                self.expected = version; self.expectedURL = url; self.expectedSize = size.uint64Value
                let manual = command["manual"] as? Bool ?? false
                let appPath = Bundle.main.bundlePath
                let parent = URL(fileURLWithPath: appPath).deletingLastPathComponent().path
                if !manual && (!FileManager.default.isWritableFile(atPath: appPath) || !FileManager.default.isWritableFile(atPath: parent)) {
                    self.report("authorization"); return
                }
                self.authorizedInstall = true
                if let resume = self.onQuit { self.onQuit = nil; resume() }
                else if let choice = self.ready { self.ready = nil; choice(.install) }
                else if let choice = self.downloadedChoice { self.downloadedChoice = nil; choice(.install) }
                else if self.updater.canCheckForUpdates { self.updater.checkForUpdates() }
                else { self.authorizedInstall = false; self.report("error") }
            }
        }
    }
    func updater(_ updater: SPUUpdater, shouldProceedWithUpdate item: SUAppcastItem, updateCheck: SPUUpdateCheck) throws {
        guard item.displayVersionString == expected, item.fileURL?.absoluteString == expectedURL, item.contentLength == expectedSize else { throw NSError(domain: "WebDeviceCable", code: 1, userInfo: [NSLocalizedDescriptionKey: "Release metadata changed. Check for updates again."]) }
    }
    func updater(_ updater: SPUUpdater, willInstallUpdateOnQuit item: SUAppcastItem, immediateInstallationBlock: @escaping () -> Void) -> Bool {
        onQuit = immediateInstallationBlock
        if authorizedInstall { DispatchQueue.main.async { self.onQuit = nil; immediateInstallationBlock() } }
        else { report("ready") }
        return true
    }
    func updater(_ updater: SPUUpdater, shouldPostponeRelaunchForUpdate item: SUAppcastItem, untilInvokingBlock installHandler: @escaping () -> Void) -> Bool {
        if authorizedInstall { return false }; onQuit = installHandler; report("ready"); return true
    }
    func updaterWillRelaunchApplication(_ updater: SPUUpdater) { UserDefaults.standard.set(true, forKey: "WDCBackgroundUpdateRelaunch") }
    func updater(_ updater: SPUUpdater, didAbortWithError error: Error) { authorizedInstall = false; report("error") }
    func updater(_ updater: SPUUpdater, shouldDownloadReleaseNotesForUpdate item: SUAppcastItem) -> Bool { false }
    func show(_ request: SPUUpdatePermissionRequest, reply: @escaping (SUUpdatePermissionResponse) -> Void) {
        reply(SUUpdatePermissionResponse(automaticUpdateChecks: false, sendSystemProfile: false))
    }
    func showUserInitiatedUpdateCheck(cancellation: @escaping () -> Void) {}
    func showUpdateFound(with appcastItem: SUAppcastItem, state: SPUUserUpdateState, reply: @escaping (SPUUserUpdateChoice) -> Void) {
        guard appcastItem.displayVersionString == expected, !appcastItem.isInformationOnlyUpdate else { reply(.dismiss); report("error"); return }
        if authorizedInstall { reply(.install) }
        else { downloadedChoice = reply; report("authorization") }
    }
    func showUpdateReleaseNotes(with downloadData: SPUDownloadData) {}
    func showUpdateReleaseNotesFailedToDownloadWithError(_ error: Error) {}
    func showUpdateNotFoundWithError(_ error: Error, acknowledgement: @escaping () -> Void) { report("error"); acknowledgement() }
    func showUpdaterError(_ error: Error, acknowledgement: @escaping () -> Void) { authorizedInstall = false; report("error"); acknowledgement() }
    func showDownloadInitiated(cancellation: @escaping () -> Void) {}
    func showDownloadDidReceiveExpectedContentLength(_ expectedContentLength: UInt64) {}
    func showDownloadDidReceiveData(ofLength length: UInt64) {}
    func showDownloadDidStartExtractingUpdate() {}
    func showExtractionReceivedProgress(_ progress: Double) {}
    func showReady(toInstallAndRelaunch reply: @escaping (SPUUserUpdateChoice) -> Void) {
        if authorizedInstall { reply(.install) } else { ready = reply; report("ready") }
    }
    func showInstallingUpdate(withApplicationTerminated applicationTerminated: Bool, retryTerminatingApplication: @escaping () -> Void) {}
    func showUpdateInstalledAndRelaunched(_ relaunched: Bool, acknowledgement: @escaping () -> Void) { acknowledgement() }
    func dismissUpdateInstallation() {}
    func showUpdateInFocus() {}
}
#endif
