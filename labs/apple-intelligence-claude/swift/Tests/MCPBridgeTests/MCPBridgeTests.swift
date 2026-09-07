import Testing
import Foundation
@testable import MCPBridge

@Suite struct JSONValueTests {
    @Test func roundTripsNestedDocument() throws {
        let source = #"{"a":1,"b":[true,null,"x"],"c":{"d":2.5}}"#
        let value = try JSONValue(jsonString: source)
        #expect(value["a"]?.intValue == 1)
        #expect(value["b"]?.arrayValue?.count == 3)
        #expect(value["c"]?["d"] == .number(2.5))
        let again = try JSONValue(jsonString: value.jsonString)
        #expect(again == value)
    }
}

@Suite struct ToolDefinitionTests {
    @Test func absentHintsAreTreatedAsSideEffecting() {
        let tool = MCPToolDefinition(name: "t", inputSchema: .object([:]))
        #expect(tool.isReadOnly == false)
        #expect(tool.isDestructive == true)
    }

    @Test func explicitReadOnlyIsNotDestructive() {
        let tool = MCPToolDefinition(
            name: "t",
            inputSchema: .object([:]),
            annotations: .init(readOnlyHint: true, destructiveHint: true)
        )
        #expect(tool.isReadOnly)
        #expect(tool.isDestructive == false)
    }

    @Test func decodesListResult() throws {
        let json = """
        {"tools":[{"name":"lookup_signins","title":"Sign-ins","description":"d",
        "inputSchema":{"type":"object","properties":{"subject":{"type":"string"}},"required":["subject"]},
        "annotations":{"readOnlyHint":true}}],"nextCursor":"n"}
        """
        let page = try JSONDecoder().decode(ToolListResult.self, from: Data(json.utf8))
        #expect(page.tools.first?.name == "lookup_signins")
        #expect(page.tools.first?.isReadOnly == true)
        #expect(page.nextCursor == "n")
    }
}

@Suite struct ToolPolicyTests {
    let readOnly = MCPToolDefinition(name: "lookup", inputSchema: .object([:]), annotations: .init(readOnlyHint: true))
    let mutating = MCPToolDefinition(name: "revoke", inputSchema: .object([:]))

    @Test func defaultPolicyExposesAllButConfirmsMutations() {
        let policy = MCPToolPolicy()
        #expect(policy.permits(readOnly))
        #expect(policy.permits(mutating))
        #expect(policy.requiresConfirmation(readOnly) == false)
        #expect(policy.requiresConfirmation(mutating))
    }

    @Test func allowAndDenyListsCompose() {
        let policy = MCPToolPolicy(allowedTools: ["lookup", "revoke"], deniedTools: ["revoke"])
        #expect(policy.permits(readOnly))
        #expect(policy.permits(mutating) == false)
    }

    @Test func defaultConfirmationDenies() async {
        let policy = MCPToolPolicy()
        #expect(await policy.confirmation(mutating, "{}") == false)
    }
}

@Suite struct SSEParserTests {
    @Test func extractsResponseByID() throws {
        let body = """
        event: message
        data: {"jsonrpc":"2.0","method":"notifications/progress","params":{}}

        data: {"jsonrpc":"2.0","id":7,"result":{"tools":[]}}

        """
        let responses = try SSEParser.responses(in: Data(body.utf8))
        #expect(responses.count == 1)
        #expect(responses.first?.id == 7)
    }
}

@Suite struct SchemaBridgeTests {
    @Test func sanitizesToolNames() {
        #expect(SchemaBridge.sanitizedName("lookup-sign.ins") == "lookup_sign_ins")
        #expect(SchemaBridge.sanitizedName("1st") == "_1st")
    }

    @Test func buildsSchemaForTypicalToolWithoutThrowing() throws {
        let tool = MCPToolDefinition(
            name: "lookup_signins",
            description: "Recent sign-ins",
            inputSchema: try JSONValue(jsonString: """
            {"type":"object","properties":{
              "subject":{"type":"string","description":"User"},
              "limit":{"type":"integer"},
              "outcome":{"type":"string","enum":["success","failure"]},
              "countries":{"type":"array","items":{"type":"string"},"maxItems":5},
              "window":{"type":"object","properties":{"hours":{"type":"number"}}}
            },"required":["subject"]}
            """)
        )
        #expect(throws: Never.self) { try SchemaBridge.generationSchema(for: tool) }
    }
}
