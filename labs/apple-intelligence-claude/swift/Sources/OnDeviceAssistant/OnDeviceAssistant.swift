import Foundation
import FoundationModels
import MCPBridge

// On-device only. This target does not link any model vendor's package: the
// keyboard extension that imports it gets Apple's SystemLanguageModel and the
// MCP gateway, nothing else.

public enum OnDeviceFailure: Error, Sendable, Equatable {
    case notConfigured
    case modelUnavailable(String)
    case contextTooSmall
    case declined
    case transport(String)
}

public actor OnDeviceAssistant {
    public static let shared = OnDeviceAssistant()

    /// Terse by necessity. Every token here is taken from the 4K-8K window that also
    /// has to hold the tool schemas, the question, the tool results, and the answer.
    public static let defaultInstructions = """
    You help an identity security engineer. Call a tool when one fits; otherwise answer \
    directly. Be correct and brief: two sentences unless asked for more.
    """

    public struct Configuration: Sendable {
        public var gateway: MCPGatewayConfiguration
        public var tools: MCPToolPolicy
        public var instructions: String
        public var toolCacheLifetime: TimeInterval
        public var maximumResponseTokens: Int

        public init(
            gateway: MCPGatewayConfiguration,
            tools: MCPToolPolicy = .readOnly,
            instructions: String = OnDeviceAssistant.defaultInstructions,
            toolCacheLifetime: TimeInterval = 600,
            maximumResponseTokens: Int = 512
        ) {
            self.gateway = gateway
            self.tools = tools
            self.instructions = instructions
            self.toolCacheLifetime = toolCacheLifetime
            self.maximumResponseTokens = maximumResponseTokens
        }
    }

    private var configuration: Configuration?
    private var client: MCPClient?
    private var definitions: [MCPToolDefinition] = []
    private var definitionsLoadedAt: Date?

    public func configure(_ configuration: Configuration) {
        self.configuration = configuration
        self.client = MCPClient(configuration: configuration.gateway)
        self.definitions = []
        self.definitionsLoadedAt = nil
    }

    /// What the caller should show when the model cannot run at all.
    public static func availabilityProblem() -> String? {
        switch SystemLanguageModel.default.availability {
        case .available:
            return nil
        case .unavailable(.deviceNotEligible):
            return "This device does not support Apple Intelligence."
        case .unavailable(.modelNotReady):
            return "The on-device model is still downloading. Try again shortly."
        case .unavailable:
            return "The on-device model is unavailable. Check Apple Intelligence in Settings."
        }
    }

    /// Answers a typed question on the on-device model, attaching only the gateway
    /// tools that fit the context window.
    public func answer(_ question: String) async throws -> String {
        guard let configuration else { throw OnDeviceFailure.notConfigured }
        if let problem = Self.availabilityProblem() {
            throw OnDeviceFailure.modelUnavailable(problem)
        }

        let model = SystemLanguageModel.default
        let instructions = Instructions(configuration.instructions)
        let instructionsCost = model.tokenCount(for: instructions)

        let selector = ToolSelector(budget: ToolBudget(contextSize: model.contextSize))
        guard selector.budget.availableForTools(instructionsCost: instructionsCost) > 0 else {
            throw OnDeviceFailure.contextTooSmall
        }

        let candidates = try await loadDefinitions(configuration)
        let selection = selector.select(
            query: question,
            from: candidates,
            instructionsCost: instructionsCost,
            measure: { model.tokenCount(for: Instructions($0)) }
        )

        guard let client else { throw OnDeviceFailure.notConfigured }
        let tools: [any Tool] = try selection.tools.map {
            try MCPTool(definition: $0, client: client, policy: configuration.tools)
        }

        let session = LanguageModelSession(model: model, tools: tools, instructions: instructions)
        let options = GenerationOptions(maximumResponseTokens: configuration.maximumResponseTokens)

        do {
            return try await session.respond(to: question, options: options).content
        } catch let error as LanguageModelError {
            switch error {
            case .contextSizeExceeded:
                throw OnDeviceFailure.contextTooSmall
            case .guardrailViolation, .refusal:
                throw OnDeviceFailure.declined
            default:
                throw OnDeviceFailure.transport(String(describing: error))
            }
        }
    }

    /// Which tools a question would pull in, for a settings screen or a debug view.
    public func plan(for question: String) async throws -> ToolSelector.Selection {
        guard let configuration else { throw OnDeviceFailure.notConfigured }
        let model = SystemLanguageModel.default
        let instructions = Instructions(configuration.instructions)
        let selector = ToolSelector(budget: ToolBudget(contextSize: model.contextSize))
        let candidates = try await loadDefinitions(configuration)
        return selector.select(
            query: question,
            from: candidates,
            instructionsCost: model.tokenCount(for: instructions),
            measure: { model.tokenCount(for: Instructions($0)) }
        )
    }

    public func refreshTools() {
        definitionsLoadedAt = nil
        definitions = []
    }

    private func loadDefinitions(_ configuration: Configuration) async throws -> [MCPToolDefinition] {
        guard let client else { throw OnDeviceFailure.notConfigured }
        if let loadedAt = definitionsLoadedAt,
           Date().timeIntervalSince(loadedAt) < configuration.toolCacheLifetime {
            return definitions
        }
        do {
            definitions = try await client.listTools().filter(configuration.tools.permits)
            definitionsLoadedAt = Date()
            return definitions
        } catch MCPError.unauthorized {
            throw OnDeviceFailure.notConfigured
        } catch {
            // A gateway that cannot be reached is not fatal: the model still answers
            // from what it knows. Cache nothing so the next call retries.
            definitions = []
            return []
        }
    }
}
