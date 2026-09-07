import Foundation
import FoundationModels
import ClaudeForFoundationModels
import MCPBridge

/// Process-wide runtime the App Intent and the UI share. Holds the gateway client,
/// caches discovered tools, and builds a routed session per request.
public actor AssistantRuntime {
    public static let shared = AssistantRuntime()

    public struct Configuration: Sendable {
        public var claude: ClaudeConfiguration
        public var gateway: MCPGatewayConfiguration
        public var routing: RoutingPolicy
        public var tools: MCPToolPolicy
        public var toolCacheLifetime: TimeInterval
        public var instructions: String

        public init(
            claude: ClaudeConfiguration,
            gateway: MCPGatewayConfiguration,
            routing: RoutingPolicy = .restricted,
            tools: MCPToolPolicy = .readOnly,
            toolCacheLifetime: TimeInterval = 600,
            instructions: String = AssistantRuntime.defaultInstructions
        ) {
            self.claude = claude
            self.gateway = gateway
            self.routing = routing
            self.tools = tools
            self.toolCacheLifetime = toolCacheLifetime
            self.instructions = instructions
        }
    }

    public static let defaultInstructions = """
    You are an assistant for an identity security engineer. You have tools provided by \
    the user's own gateway. Prefer calling a tool over guessing. Read-only tools may be \
    called freely. Tools that change state require the user's approval, which the tool \
    itself will request; if approval is refused, say so and stop. Answer in two or three \
    sentences suitable for being read aloud, then offer detail only if asked.
    """

    private var configuration: Configuration?
    private var client: MCPClient?
    private var cachedTools: [any Tool] = []
    private var toolsLoadedAt: Date?

    public func configure(_ configuration: Configuration) {
        self.configuration = configuration
        self.client = MCPClient(configuration: configuration.gateway)
        self.cachedTools = []
        self.toolsLoadedAt = nil
    }

    /// Answers a request, using gateway tools on whichever model the routing policy picks.
    public func answer(_ request: String) async throws -> String {
        guard let configuration else { throw AssistantFailure.notConfigured }
        let tools = try await loadToolsIfNeeded(configuration)
        let router = ModelRouter(policy: configuration.routing, claude: configuration.claude.makeModel())
        return try await router.perform(prompt: request, tools: tools, instructions: configuration.instructions) { session in
            try await session.respond(to: request).content
        }
    }

    /// Drops the tool cache so the next request re-runs `tools/list`.
    public func refreshTools() {
        toolsLoadedAt = nil
        cachedTools = []
    }

    private func loadToolsIfNeeded(_ configuration: Configuration) async throws -> [any Tool] {
        guard let client else { throw AssistantFailure.notConfigured }
        if let loadedAt = toolsLoadedAt, Date().timeIntervalSince(loadedAt) < configuration.toolCacheLifetime {
            return cachedTools
        }
        do {
            cachedTools = try await MCPToolset.load(from: client, policy: configuration.tools)
            toolsLoadedAt = Date()
            return cachedTools
        } catch MCPError.unauthorized {
            throw AssistantFailure.notConfigured
        }
    }
}
