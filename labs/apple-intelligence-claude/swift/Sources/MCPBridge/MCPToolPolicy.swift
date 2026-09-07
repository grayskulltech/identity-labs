import Foundation

/// Which gateway tools the model may see, and which calls need a human in the loop.
/// Fails closed: with the default configuration a tool that is not marked read-only
/// is never executed, because the default confirmation handler denies everything.
public struct MCPToolPolicy: Sendable {
    public typealias ConfirmationHandler = @Sendable (_ tool: MCPToolDefinition, _ argumentsJSON: String) async -> Bool

    /// Names the model may see. `nil` exposes every tool the gateway lists.
    public var allowedTools: Set<String>?

    /// Names that are never exposed, applied after `allowedTools`.
    public var deniedTools: Set<String>

    /// Require confirmation for any tool the server did not mark read-only.
    public var confirmNonReadOnly: Bool

    /// Called before a non-read-only tool runs. Wire it to UI or to an App Intent
    /// confirmation. The default denies.
    public var confirmation: ConfirmationHandler

    public init(
        allowedTools: Set<String>? = nil,
        deniedTools: Set<String> = [],
        confirmNonReadOnly: Bool = true,
        confirmation: @escaping ConfirmationHandler = { _, _ in false }
    ) {
        self.allowedTools = allowedTools
        self.deniedTools = deniedTools
        self.confirmNonReadOnly = confirmNonReadOnly
        self.confirmation = confirmation
    }

    /// Read-only tools only. The right starting point for a Siri-facing assistant.
    public static let readOnly = MCPToolPolicy(confirmNonReadOnly: true)

    public func permits(_ tool: MCPToolDefinition) -> Bool {
        if deniedTools.contains(tool.name) { return false }
        if let allowedTools, !allowedTools.contains(tool.name) { return false }
        return true
    }

    public func requiresConfirmation(_ tool: MCPToolDefinition) -> Bool {
        confirmNonReadOnly && !tool.isReadOnly
    }
}
