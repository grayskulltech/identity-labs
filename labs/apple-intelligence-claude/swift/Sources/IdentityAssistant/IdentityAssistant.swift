import Foundation
import FoundationModels
import ClaudeForFoundationModels

/// Facade over the router, the tool, and the structured output. One call from the UI.
public struct IdentityAssistant: Sendable {
    public static let instructions = """
    You are an identity security analyst's assistant. You assess sign-in activity for \
    account compromise. Always call recentSignInEvents before giving a verdict. Judge \
    risk from impossible travel, MFA failures followed by success, unfamiliar client \
    applications, and bursts of failures. Be specific about which events drove the \
    verdict. Do not speculate beyond the events returned.
    """

    private let router: ModelRouter
    private let tool: SignInEventsTool

    public init(configuration: ClaudeConfiguration, policy: RoutingPolicy, events: any SignInEventSource) {
        self.router = ModelRouter(policy: policy, claude: configuration.makeModel())
        self.tool = SignInEventsTool(source: events)
    }

    /// Returns a structured verdict for the analyst's request.
    public func triage(_ request: String) async throws -> TriageVerdict {
        try await router.perform(prompt: request, tools: [tool], instructions: Self.instructions) { session in
            let response = try await session.respond(to: request, generating: TriageVerdict.self)
            return response.content
        }
    }

    /// Streams a plain-language explanation. Each element is the cumulative text so far.
    public func explain(_ request: String) -> AsyncThrowingStream<String, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    let session = router.session(for: request, tools: [tool], instructions: Self.instructions)
                    for try await partial in session.streamResponse(to: request) {
                        continuation.yield(partial.content)
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    /// Which model a request would use under the current policy, for display in the UI
    /// so the analyst knows before sending whether the request leaves the device.
    public func destination(for request: String) -> ModelRouter.Route {
        router.route(for: request)
    }
}

/// Maps errors to operator-facing guidance. Order matters: configuration errors first,
/// framework-shaped errors next, everything else is transport.
public enum AssistantFailure: Error, Sendable, Equatable {
    case notConfigured
    case rateLimited
    case contextTooLarge
    case declined
    case transport(String)

    public init(_ error: any Error) {
        if case ClaudeError.missingCredential = error {
            self = .notConfigured
            return
        }
        if let framework = error as? LanguageModelError {
            switch framework {
            case .rateLimited:
                self = .rateLimited
            case .contextSizeExceeded:
                self = .contextTooLarge
            case .guardrailViolation, .refusal:
                self = .declined
            default:
                self = .transport(String(describing: framework))
            }
            return
        }
        self = .transport(String(describing: error))
    }
}
