import Foundation

/// A tool as advertised by the gateway's `tools/list`.
public struct MCPToolDefinition: Sendable, Equatable, Codable {
    public struct Annotations: Sendable, Equatable, Codable {
        public var readOnlyHint: Bool?
        public var destructiveHint: Bool?
        public var idempotentHint: Bool?
        public var openWorldHint: Bool?

        public init(readOnlyHint: Bool? = nil, destructiveHint: Bool? = nil, idempotentHint: Bool? = nil, openWorldHint: Bool? = nil) {
            self.readOnlyHint = readOnlyHint
            self.destructiveHint = destructiveHint
            self.idempotentHint = idempotentHint
            self.openWorldHint = openWorldHint
        }
    }

    public var name: String
    public var title: String?
    public var description: String?
    public var inputSchema: JSONValue
    public var annotations: Annotations?

    public init(name: String, title: String? = nil, description: String? = nil, inputSchema: JSONValue, annotations: Annotations? = nil) {
        self.name = name
        self.title = title
        self.description = description
        self.inputSchema = inputSchema
        self.annotations = annotations
    }

    /// True only when the server explicitly marks the tool read-only. Absent hints are
    /// treated as "may have side effects", which is the spec's default and the safe one.
    public var isReadOnly: Bool {
        annotations?.readOnlyHint == true
    }

    /// The spec defaults `destructiveHint` to true when absent, so a tool is treated as
    /// destructive unless it is read-only or the hint is explicitly false.
    public var isDestructive: Bool {
        if isReadOnly { return false }
        return annotations?.destructiveHint ?? true
    }
}

/// Result of `tools/call`, reduced to what the model needs.
public struct MCPToolResult: Sendable, Equatable {
    public var text: String
    public var structuredContent: JSONValue?
    public var isError: Bool

    public init(text: String, structuredContent: JSONValue? = nil, isError: Bool = false) {
        self.text = text
        self.structuredContent = structuredContent
        self.isError = isError
    }

    /// What goes back into the transcript. Errors are labelled so the model can recover
    /// instead of treating an error string as data.
    public var textForModel: String {
        if isError { return "Tool error: \(text)" }
        if let structured = structuredContent { return structured.jsonString }
        return text
    }
}

public enum MCPError: Error, Sendable, Equatable {
    case notConfigured
    case unauthorized
    case httpStatus(Int)
    case sessionExpired
    case invalidResponse(String)
    case rpc(code: Int, message: String)
}
