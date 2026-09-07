import Foundation

/// Supplies the bearer token the gateway expects. Back it with Keychain storage and
/// your OAuth flow; the client never caches the token itself.
public protocol MCPTokenProvider: Sendable {
    func accessToken() async throws -> String?
}

public struct StaticTokenProvider: MCPTokenProvider {
    private let token: String?

    public init(_ token: String?) {
        self.token = token
    }

    public func accessToken() async throws -> String? {
        token
    }
}

public struct MCPGatewayConfiguration: Sendable {
    public var endpoint: URL
    public var tokenProvider: any MCPTokenProvider
    public var clientName: String
    public var clientVersion: String
    public var protocolVersion: String

    public init(
        endpoint: URL,
        tokenProvider: any MCPTokenProvider,
        clientName: String = "IdentityAssistant",
        clientVersion: String = "1.0",
        protocolVersion: String = "2025-06-18"
    ) {
        self.endpoint = endpoint
        self.tokenProvider = tokenProvider
        self.clientName = clientName
        self.clientVersion = clientVersion
        self.protocolVersion = protocolVersion
    }
}

/// Streamable HTTP client for a single MCP server. Handles the initialize handshake,
/// session IDs, JSON and SSE response bodies, and re-initialization after a 404.
public actor MCPClient {
    private let configuration: MCPGatewayConfiguration
    private let session: URLSession
    private var sessionID: String?
    private var nextID = 1
    private var initialized = false

    public init(configuration: MCPGatewayConfiguration, session: URLSession = .shared) {
        self.configuration = configuration
        self.session = session
    }

    // MARK: Public API

    public func listTools() async throws -> [MCPToolDefinition] {
        try await ensureInitialized()
        var tools: [MCPToolDefinition] = []
        var cursor: JSONValue?
        repeat {
            var params: [String: JSONValue] = [:]
            if let cursor { params["cursor"] = cursor }
            let result = try await request(method: "tools/list", params: .object(params))
            let page = try JSONDecoder().decode(ToolListResult.self, from: Data(result.jsonString.utf8))
            tools.append(contentsOf: page.tools)
            cursor = page.nextCursor.map { .string($0) }
        } while cursor != nil
        return tools
    }

    public func callTool(name: String, argumentsJSON: String) async throws -> MCPToolResult {
        try await ensureInitialized()
        let arguments = (try? JSONValue(jsonString: argumentsJSON)) ?? .object([:])
        let result = try await request(method: "tools/call", params: .object(["name": .string(name), "arguments": arguments]))
        let content = result["content"]?.arrayValue ?? []
        let text = content.compactMap { item -> String? in
            guard item["type"]?.stringValue == "text" else { return nil }
            return item["text"]?.stringValue
        }.joined(separator: "\n")
        return MCPToolResult(
            text: text,
            structuredContent: result["structuredContent"],
            isError: result["isError"]?.boolValue ?? false
        )
    }

    /// Ends the session on the server. Safe to call when no session exists.
    public func close() async {
        guard let sessionID else { return }
        var req = URLRequest(url: configuration.endpoint)
        req.httpMethod = "DELETE"
        req.setValue(sessionID, forHTTPHeaderField: "Mcp-Session-Id")
        req.setValue(configuration.protocolVersion, forHTTPHeaderField: "MCP-Protocol-Version")
        if let token = try? await configuration.tokenProvider.accessToken() {
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        _ = try? await session.data(for: req)
        self.sessionID = nil
        initialized = false
    }

    // MARK: Handshake

    private func ensureInitialized() async throws {
        if initialized { return }
        sessionID = nil
        let params: JSONValue = .object([
            "protocolVersion": .string(configuration.protocolVersion),
            "capabilities": .object([:]),
            "clientInfo": .object([
                "name": .string(configuration.clientName),
                "version": .string(configuration.clientVersion),
            ]),
        ])
        _ = try await request(method: "initialize", params: params, allowReinitialize: false)
        try await notify(method: "notifications/initialized")
        initialized = true
    }

    // MARK: Transport

    private func request(method: String, params: JSONValue, allowReinitialize: Bool = true) async throws -> JSONValue {
        let id = nextID
        nextID += 1
        let envelope = JSONRPCRequest(id: id, method: method, params: params)
        let (data, response) = try await send(try JSONEncoder().encode(envelope))

        switch response.statusCode {
        case 200:
            break
        case 401, 403:
            throw MCPError.unauthorized
        case 404 where allowReinitialize && sessionID != nil:
            initialized = false
            try await ensureInitialized()
            return try await request(method: method, params: params, allowReinitialize: false)
        default:
            throw MCPError.httpStatus(response.statusCode)
        }

        if method == "initialize", let assigned = response.value(forHTTPHeaderField: "Mcp-Session-Id") {
            sessionID = assigned
        }

        let contentType = response.value(forHTTPHeaderField: "Content-Type") ?? ""
        let message: JSONRPCResponse
        if contentType.hasPrefix("text/event-stream") {
            guard let found = try SSEParser.responses(in: data).first(where: { $0.id == id }) else {
                throw MCPError.invalidResponse("No JSON-RPC response for id \(id) in event stream")
            }
            message = found
        } else {
            message = try JSONDecoder().decode(JSONRPCResponse.self, from: data)
        }

        if let error = message.error {
            throw MCPError.rpc(code: error.code, message: error.message)
        }
        guard let result = message.result else {
            throw MCPError.invalidResponse("Response carried neither result nor error")
        }
        return result
    }

    private func notify(method: String) async throws {
        let envelope = JSONRPCNotification(method: method)
        let (_, response) = try await send(try JSONEncoder().encode(envelope))
        guard (200...299).contains(response.statusCode) else {
            throw MCPError.httpStatus(response.statusCode)
        }
    }

    private func send(_ body: Data) async throws -> (Data, HTTPURLResponse) {
        var req = URLRequest(url: configuration.endpoint)
        req.httpMethod = "POST"
        req.httpBody = body
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("application/json, text/event-stream", forHTTPHeaderField: "Accept")
        req.setValue(configuration.protocolVersion, forHTTPHeaderField: "MCP-Protocol-Version")
        if let sessionID {
            req.setValue(sessionID, forHTTPHeaderField: "Mcp-Session-Id")
        }
        if let token = try await configuration.tokenProvider.accessToken() {
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        let (data, response) = try await session.data(for: req)
        guard let http = response as? HTTPURLResponse else {
            throw MCPError.invalidResponse("Not an HTTP response")
        }
        return (data, http)
    }
}

// MARK: Wire types

struct JSONRPCRequest: Encodable {
    var jsonrpc = "2.0"
    var id: Int
    var method: String
    var params: JSONValue
}

struct JSONRPCNotification: Encodable {
    var jsonrpc = "2.0"
    var method: String
}

struct JSONRPCResponse: Decodable {
    struct RPCError: Decodable {
        var code: Int
        var message: String
    }

    var id: Int?
    var result: JSONValue?
    var error: RPCError?
}

struct ToolListResult: Decodable {
    var tools: [MCPToolDefinition]
    var nextCursor: String?
}

/// Extracts JSON-RPC responses from a buffered Server-Sent Events body.
enum SSEParser {
    static func responses(in data: Data) throws -> [JSONRPCResponse] {
        guard let text = String(data: data, encoding: .utf8) else { return [] }
        let decoder = JSONDecoder()
        var responses: [JSONRPCResponse] = []
        for event in text.components(separatedBy: "\n\n") {
            let payload = event
                .split(separator: "\n", omittingEmptySubsequences: true)
                .filter { $0.hasPrefix("data:") }
                .map { $0.dropFirst(5).trimmingCharacters(in: .whitespaces) }
                .joined(separator: "\n")
            guard !payload.isEmpty else { continue }
            if let response = try? decoder.decode(JSONRPCResponse.self, from: Data(payload.utf8)), response.id != nil {
                responses.append(response)
            }
        }
        return responses
    }
}
