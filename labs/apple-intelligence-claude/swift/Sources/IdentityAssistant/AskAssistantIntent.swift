import AppIntents
import Foundation

/// The Siri entry point. Siri resolves the phrase, collects the request text, and
/// calls `perform()`. The intent runs in the app's process, so `AssistantRuntime`
/// must be configured at launch (or lazily from a stored configuration).
public struct AskAssistantIntent: AppIntent {
    public static let title: LocalizedStringResource = "Ask Identity Assistant"
    public static let description = IntentDescription(
        "Asks the assistant a question. It can use the tools exposed by your MCP gateway."
    )
    public static let openAppWhenRun = false

    @Parameter(title: "Request", requestValueDialog: "What do you need?")
    public var request: String

    public init() {}

    public init(request: String) {
        self.request = request
    }

    public func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<String> {
        do {
            let answer = try await AssistantRuntime.shared.answer(request)
            return .result(value: answer, dialog: "\(answer)")
        } catch {
            let failure = AssistantFailure(error)
            let message: String
            switch failure {
            case .notConfigured: message = "The assistant is not signed in. Open the app to connect it."
            case .rateLimited: message = "The assistant is busy. Try again in a moment."
            case .contextTooLarge: message = "That request is too large. Try a narrower question."
            case .declined: message = "The assistant declined that request."
            case .transport(let detail): message = "The assistant could not reach its tools. \(detail)"
            }
            return .result(value: message, dialog: "\(message)")
        }
    }
}

/// Register this package from the app's own `AppIntentsPackage` so the intent is
/// discoverable from a library target:
///
/// ```swift
/// struct AppPackage: AppIntentsPackage {
///     static var includedPackages: [any AppIntentsPackage.Type] { [IdentityAssistantIntents.self] }
/// }
/// ```
///
/// `AppShortcutsProvider` must live in the app target, not here. Example:
///
/// ```swift
/// struct AssistantShortcuts: AppShortcutsProvider {
///     static var appShortcuts: [AppShortcut] {
///         AppShortcut(
///             intent: AskAssistantIntent(),
///             phrases: ["Ask \(.applicationName) \(\.$request)", "Ask \(.applicationName)"],
///             shortTitle: "Ask",
///             systemImageName: "person.badge.key"
///         )
///     }
/// }
/// ```
public struct IdentityAssistantIntents: AppIntentsPackage {}
