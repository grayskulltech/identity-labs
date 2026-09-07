import Testing
import Foundation
import MCPBridge
@testable import OnDeviceAssistant

// Selection and budgeting are pure: the tokenizer is injected, so these run without
// the Foundation Models framework or an Apple Intelligence device.

private func tool(_ name: String, title: String? = nil, description: String? = nil, schemaPadding: Int = 0) -> MCPToolDefinition {
    // Built programmatically so the padding really lands inside a valid schema and
    // the billable text grows with it.
    let schema = JSONValue.object([
        "type": .string("object"),
        "properties": .object([
            "subject": .object([
                "type": .string("string"),
                "description": .string(String(repeating: "x", count: schemaPadding)),
            ]),
        ]),
    ])
    return MCPToolDefinition(
        name: name,
        title: title,
        description: description,
        inputSchema: schema,
        annotations: .init(readOnlyHint: true)
    )
}

/// Stand-in tokenizer: roughly four characters per token, like a real one.
private let measure: (String) -> Int = { max(1, $0.count / 4) }

@Suite struct ToolRelevanceTests {
    let relevance = ToolRelevance()

    @Test func namesOutweighDescriptions() {
        let named = tool("revoke_sessions", description: "unrelated text")
        let described = tool("unrelated_name", description: "revoke sessions for a user")
        let query = "revoke sessions"
        #expect(relevance.score(query: query, tool: named) > relevance.score(query: query, tool: described))
    }

    /// Known limitation, asserted so it is not mistaken for a bug: a compound tool
    /// name stems as one term, so `lookup_signins` does not match "sign-ins". Term
    /// matching finds tools through their descriptions, not their identifiers —
    /// which is why descriptions on the gateway need to read like English.
    @Test func compoundNamesDoNotMatchSplitQueryTerms() {
        let compound = tool("lookup_signins", description: "unrelated text")
        #expect(relevance.score(query: "look up sign-ins", tool: compound) == 0)

        let described = tool("lookup_signins", description: "recent sign in events")
        #expect(relevance.score(query: "recent sign in events", tool: described) > 0)
    }

    @Test func unrelatedToolScoresZero() {
        #expect(relevance.score(query: "what is the weather", tool: tool("revoke_sessions", description: "revoke every session")) == 0)
    }

    @Test func stemmingMatchesPluralAndGerund() {
        let terms = ToolRelevance.terms("revoking sessions")
        #expect(terms.contains("revok"))
        #expect(terms.contains("session"))
    }

    @Test func shortWordsAreIgnored() {
        #expect(ToolRelevance.terms("is a to the of").isEmpty)
    }
}

@Suite struct ToolBudgetTests {
    @Test func availableSubtractsReservationsAndInstructions() {
        let budget = ToolBudget(contextSize: 4096, reservedForOutput: 512, reservedForWorkingSet: 768)
        #expect(budget.availableForTools(instructionsCost: 100) == 4096 - 512 - 768 - 100)
    }

    @Test func billableTextIncludesSchema() {
        let text = ToolBudget.billableText(for: tool("t", description: "d"))
        #expect(text.contains("t"))
        #expect(text.contains("d"))
        #expect(text.contains("object"))
    }
}

@Suite struct ToolSelectorTests {
    let candidates = [
        tool("lookup_signins", description: "recent sign in events for a user", schemaPadding: 400),
        tool("revoke_sessions", description: "revoke sessions for a user", schemaPadding: 400),
        tool("list_printers", description: "office printers", schemaPadding: 400),
    ]

    @Test func picksOnlyRelevantTools() {
        let selector = ToolSelector(budget: ToolBudget(contextSize: 4096))
        let selection = selector.select(query: "recent sign in events", from: candidates, instructionsCost: 40, measure: measure)
        #expect(selection.tools.map(\.name) == ["lookup_signins"])
        #expect(selection.dropped.isEmpty)
    }

    @Test func stopsAtTheMaximumToolCount() {
        let selector = ToolSelector(budget: ToolBudget(contextSize: 8192, maximumTools: 1))
        let selection = selector.select(query: "sign in sessions user", from: candidates, instructionsCost: 40, measure: measure)
        #expect(selection.tools.count == 1)
        #expect(selection.dropped.count == 1, "the second matching tool is reported as dropped")
    }

    @Test func dropsToolsThatDoNotFit() {
        // Window large enough for instructions and reservations, but not for a schema.
        let budget = ToolBudget(contextSize: 1400, reservedForOutput: 512, reservedForWorkingSet: 768)
        let selector = ToolSelector(budget: budget)
        let selection = selector.select(query: "sign in", from: candidates, instructionsCost: 40, measure: measure)
        #expect(selection.tools.isEmpty)
        #expect(selection.dropped.contains("lookup_signins"))
    }

    @Test func reportsEverythingDroppedWhenNoRoomRemains() {
        let selector = ToolSelector(budget: ToolBudget(contextSize: 1000, reservedForOutput: 512, reservedForWorkingSet: 768))
        let selection = selector.select(query: "sign in", from: candidates, instructionsCost: 10, measure: measure)
        #expect(selection.tools.isEmpty)
        #expect(selection.dropped.count == candidates.count)
        #expect(selection.tokensAvailable == 0)
    }

    @Test func selectionIsDeterministicForTiedScores() {
        let tied = [tool("beta_user", description: "user"), tool("alpha_user", description: "user")]
        let selector = ToolSelector(budget: ToolBudget(contextSize: 8192))
        let first = selector.select(query: "user", from: tied, instructionsCost: 10, measure: measure)
        let second = selector.select(query: "user", from: tied, instructionsCost: 10, measure: measure)
        #expect(first.tools.map(\.name) == second.tools.map(\.name))
        #expect(first.tools.first?.name == "alpha_user", "ties break alphabetically")
    }
}
