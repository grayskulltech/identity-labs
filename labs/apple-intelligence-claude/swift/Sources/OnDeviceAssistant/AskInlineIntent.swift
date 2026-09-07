import AppIntents
import Foundation

/// Typed entry point. Appears in Spotlight and in Shortcuts, so a question can be
/// asked by typing rather than speaking. Runs entirely on device.
public struct AskInlineIntent: AppIntent {
    public static let title: LocalizedStringResource = "Ask On-Device Assistant"
    public static let description = IntentDescription(
        "Answers a typed question using the on-device model and your gateway's tools. Nothing leaves the device except the tool calls you allow."
    )
    public static let openAppWhenRun = false

    @Parameter(title: "Question", requestValueDialog: "What do you want to ask?")
    public var question: String

    public init() {}

    public init(question: String) {
        self.question = question
    }

    public func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<String> {
        let answer: String
        do {
            answer = try await OnDeviceAssistant.shared.answer(question)
        } catch let failure as OnDeviceFailure {
            answer = Self.message(for: failure)
        } catch {
            answer = "The assistant could not finish that request."
        }
        return .result(value: answer, dialog: "\(answer)")
    }

    static func message(for failure: OnDeviceFailure) -> String {
        switch failure {
        case .notConfigured:
            return "The assistant is not connected to your gateway. Open the app to sign in."
        case .modelUnavailable(let reason):
            return reason
        case .contextTooSmall:
            return "That question needs more context than the on-device model has. Try a narrower question, or reduce the tool allowlist."
        case .declined:
            return "The on-device model declined that request."
        case .transport(let detail):
            return "The assistant could not finish that request. \(detail)"
        }
    }
}

/// Register from the app's own `AppIntentsPackage`:
///
/// ```swift
/// struct AppPackage: AppIntentsPackage {
///     static var includedPackages: [any AppIntentsPackage.Type] { [OnDeviceAssistantIntents.self] }
/// }
/// ```
public struct OnDeviceAssistantIntents: AppIntentsPackage {}
