import Foundation
import FoundationModels
import ClaudeForFoundationModels

/// Where a request is allowed to run. Policy is data, so it can be loaded from a
/// managed app configuration instead of being compiled in.
public struct RoutingPolicy: Sendable, Equatable {
    /// When false, any prompt the classifier flags as carrying identity PII stays on-device.
    public var allowsCloudForPII: Bool

    /// When true, a Claude rate limit falls back to the on-device model for that turn
    /// instead of surfacing an error.
    public var fallsBackOnRateLimit: Bool

    public init(allowsCloudForPII: Bool = false, fallsBackOnRateLimit: Bool = true) {
        self.allowsCloudForPII = allowsCloudForPII
        self.fallsBackOnRateLimit = fallsBackOnRateLimit
    }

    /// Supervised device handling regulated identity data.
    public static let restricted = RoutingPolicy(allowsCloudForPII: false, fallsBackOnRateLimit: true)

    /// Organization has an approved AI-use policy covering identity data.
    public static let permissive = RoutingPolicy(allowsCloudForPII: true, fallsBackOnRateLimit: true)
}

/// Conservative, regex-based detector for identity identifiers in free text.
/// Replace with your DLP engine's verdict when integrating; keep the interface.
public struct PIIClassifier: Sendable {
    public enum Signal: String, Sendable, CaseIterable {
        case emailAddress
        case ipv4Address
        case uuidIdentifier
        case bearerToken
        case userPrincipalName
    }

    private static let patterns: [(Signal, NSRegularExpression)] = {
        let sources: [(Signal, String)] = [
            (.emailAddress, #"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}"#),
            (.ipv4Address, #"\b(?:\d{1,3}\.){3}\d{1,3}\b"#),
            (.uuidIdentifier, #"\b[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\b"#),
            (.bearerToken, #"\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}"#),
            (.userPrincipalName, #"\b[A-Za-z0-9._-]+\\[A-Za-z0-9._-]+\b"#),
        ]
        return sources.compactMap { signal, source in
            guard let regex = try? NSRegularExpression(pattern: source) else { return nil }
            return (signal, regex)
        }
    }()

    public init() {}

    public func signals(in text: String) -> Set<Signal> {
        let range = NSRange(text.startIndex..., in: text)
        var found = Set<Signal>()
        for (signal, regex) in Self.patterns where regex.firstMatch(in: text, range: range) != nil {
            found.insert(signal)
        }
        return found
    }

    public func containsPII(_ text: String) -> Bool {
        !signals(in: text).isEmpty
    }
}

/// Selects a `LanguageModel` per request and applies the fallback rule.
public struct ModelRouter: Sendable {
    public enum Route: Sendable, Equatable {
        case onDevice
        case claude
    }

    public var policy: RoutingPolicy
    public var classifier: PIIClassifier
    private let claude: ClaudeLanguageModel

    public init(policy: RoutingPolicy, claude: ClaudeLanguageModel, classifier: PIIClassifier = PIIClassifier()) {
        self.policy = policy
        self.claude = claude
        self.classifier = classifier
    }

    /// Pure decision, separated from execution so it can be unit tested without a network.
    public func route(for prompt: String) -> Route {
        if classifier.containsPII(prompt) && !policy.allowsCloudForPII {
            return .onDevice
        }
        return .claude
    }

    /// Creates a session bound to the chosen model. Tools and instructions are shared,
    /// so the app's behavior is the same on either model apart from capability.
    public func session(for prompt: String, tools: [any Tool], instructions: String) -> LanguageModelSession {
        switch route(for: prompt) {
        case .onDevice:
            return LanguageModelSession(model: SystemLanguageModel.default, tools: tools, instructions: instructions)
        case .claude:
            return LanguageModelSession(model: claude, tools: tools, instructions: instructions)
        }
    }

    /// Runs `operation` against the routed session. On a Claude rate limit, and only when
    /// the policy allows it, the same operation is retried once on the on-device model.
    public func perform<T: Sendable>(
        prompt: String,
        tools: [any Tool],
        instructions: String,
        operation: (LanguageModelSession) async throws -> T
    ) async throws -> T {
        let primary = session(for: prompt, tools: tools, instructions: instructions)
        do {
            return try await operation(primary)
        } catch LanguageModelError.rateLimited where policy.fallsBackOnRateLimit && route(for: prompt) == .claude {
            let fallback = LanguageModelSession(model: SystemLanguageModel.default, tools: tools, instructions: instructions)
            return try await operation(fallback)
        }
    }
}
