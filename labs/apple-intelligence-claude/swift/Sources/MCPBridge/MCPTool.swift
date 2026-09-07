import Foundation
import FoundationModels

/// A Foundation Models tool backed by one MCP gateway tool. Arguments are whatever
/// the model generated against the gateway's schema; they are forwarded as JSON.
public struct MCPTool: Tool {
    public typealias Arguments = GeneratedContent
    public typealias Output = String

    public let definition: MCPToolDefinition
    public let name: String
    public let description: String
    public let parameters: GenerationSchema

    private let client: MCPClient
    private let policy: MCPToolPolicy

    public init(definition: MCPToolDefinition, client: MCPClient, policy: MCPToolPolicy) throws {
        self.definition = definition
        self.client = client
        self.policy = policy
        self.name = definition.name
        self.description = Self.describe(definition)
        self.parameters = try SchemaBridge.generationSchema(for: definition)
    }

    public func call(arguments: GeneratedContent) async throws -> String {
        let argumentsJSON = arguments.jsonString

        if policy.requiresConfirmation(definition) {
            let approved = await policy.confirmation(definition, argumentsJSON)
            guard approved else {
                return "The user did not approve running \(definition.name). Do not retry it; explain what it would have done."
            }
        }

        do {
            let result = try await client.callTool(name: definition.name, argumentsJSON: argumentsJSON)
            return result.textForModel
        } catch MCPError.unauthorized {
            return "Tool error: the gateway rejected the credential. Tell the user to sign in again."
        } catch let error as MCPError {
            return "Tool error: \(String(describing: error))"
        }
    }

    /// The description is the only place the model learns about side effects, so the
    /// annotations are spelled out rather than left implicit.
    static func describe(_ definition: MCPToolDefinition) -> String {
        var parts: [String] = []
        if let title = definition.title { parts.append(title + ".") }
        if let description = definition.description { parts.append(description) }
        if definition.isReadOnly {
            parts.append("Read-only.")
        } else if definition.isDestructive {
            parts.append("Makes changes that may not be reversible; requires user approval.")
        } else {
            parts.append("Has side effects; requires user approval.")
        }
        return parts.joined(separator: " ")
    }
}

/// Discovers the gateway's tools and wraps the permitted ones.
public enum MCPToolset {
    public static func load(from client: MCPClient, policy: MCPToolPolicy) async throws -> [any Tool] {
        let definitions = try await client.listTools()
        var tools: [any Tool] = []
        for definition in definitions where policy.permits(definition) {
            tools.append(try MCPTool(definition: definition, client: client, policy: policy))
        }
        return tools
    }
}
