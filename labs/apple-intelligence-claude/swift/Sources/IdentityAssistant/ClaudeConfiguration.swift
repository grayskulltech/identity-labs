import Foundation
import FoundationModels
import ClaudeForFoundationModels

/// How the app authenticates to the Claude API. Mirrors the package's `AuthMode`
/// but forces the caller to name the deployment posture, so a development key
/// cannot be passed where a production mode is expected.
public enum ClaudeAuth: Sendable {
    /// Production, direct to Anthropic. The device attests itself with App Attest and
    /// receives short-lived tokens scoped to your workspace. No secret ships in the app.
    case appAttest(clientID: String)

    /// Production, through your own relay. The relay attaches the API key and can
    /// enforce a model allowlist and per-user authorization. See `relay/` in this lab.
    case proxied(baseURL: URL, appToken: String)

    /// Development only. The key is extractable from any shipped binary.
    case developmentAPIKey(String)
}

/// Typed configuration for the Claude side of the assistant.
public struct ClaudeConfiguration: Sendable {
    public var auth: ClaudeAuth
    public var model: ClaudeModel
    public var serverTools: [ClaudeServerTool]

    /// Defaults to Claude Opus 5 with no server-side tools. Identity triage is a
    /// reasoning task over data the app already holds, so web search and code
    /// execution are opt-in.
    public init(
        auth: ClaudeAuth,
        model: ClaudeModel = .opus5,
        serverTools: [ClaudeServerTool] = []
    ) {
        self.auth = auth
        self.model = model
        self.serverTools = serverTools
    }

    /// Builds the provider. `.proxied` sets `baseURL` so every request goes to the relay,
    /// which receives standard Messages API requests and forwards them to Anthropic.
    public func makeModel() -> ClaudeLanguageModel {
        switch auth {
        case .appAttest(let clientID):
            return ClaudeLanguageModel(
                name: model,
                auth: .appAttest(clientID: clientID),
                serverTools: serverTools
            )
        case .proxied(let baseURL, let appToken):
            return ClaudeLanguageModel(
                name: model,
                auth: .proxied(headers: ["Authorization": "Bearer \(appToken)"]),
                baseURL: baseURL,
                serverTools: serverTools
            )
        case .developmentAPIKey(let key):
            return ClaudeLanguageModel(
                name: model,
                auth: .apiKey(key),
                serverTools: serverTools
            )
        }
    }

    /// Reads `ANTHROPIC_API_KEY` for Simulator and command-line runs. Returns nil when
    /// the variable is absent so callers fail closed instead of sending an empty key.
    public static func development() -> ClaudeConfiguration? {
        guard let key = ProcessInfo.processInfo.environment["ANTHROPIC_API_KEY"], !key.isEmpty else {
            return nil
        }
        return ClaudeConfiguration(auth: .developmentAPIKey(key))
    }
}
