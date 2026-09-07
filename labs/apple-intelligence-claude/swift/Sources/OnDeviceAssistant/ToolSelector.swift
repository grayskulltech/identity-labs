import Foundation
import MCPBridge

// Apple's on-device model has a small fixed context: instructions, tool schemas,
// prompt, and output all share it. A gateway with a dozen tools will not fit, so
// tools are ranked against the query and admitted until the budget is spent.
//
// Scoring and budgeting are pure and take token measurement as a closure, so they
// are testable without the Foundation Models framework.

/// Ranks a gateway's tools against the text the person typed.
public struct ToolRelevance: Sendable {
    public init() {}

    /// Term overlap between the query and the tool's name, title, and description.
    /// Name matches weigh most: a person asking to "look up sign-ins" is naming the tool.
    public func score(query: String, tool: MCPToolDefinition) -> Double {
        let queryTerms = Self.terms(query)
        guard !queryTerms.isEmpty else { return 0 }

        let nameTerms = Self.terms(tool.name)
        let titleTerms = Self.terms(tool.title ?? "")
        let descriptionTerms = Self.terms(tool.description ?? "")

        let nameHits = Double(queryTerms.intersection(nameTerms).count)
        let titleHits = Double(queryTerms.intersection(titleTerms).count)
        let descriptionHits = Double(queryTerms.intersection(descriptionTerms).count)

        return (nameHits * 3.0) + (titleHits * 2.0) + descriptionHits
    }

    /// Lowercased word stems, split on anything that is not a letter or digit so
    /// `lookup_signins` and "look up sign-ins" reduce to comparable terms.
    static func terms(_ text: String) -> Set<String> {
        let pieces = text.lowercased().split { !$0.isLetter && !$0.isNumber }
        var result = Set<String>()
        for piece in pieces where piece.count > 2 {
            var term = String(piece)
            for suffix in ["ing", "ies", "es", "s"] where term.count > 4 && term.hasSuffix(suffix) {
                term = String(term.dropLast(suffix.count))
                break
            }
            result.insert(term)
        }
        return result
    }
}

/// Decides how many ranked tools actually fit the context window.
public struct ToolBudget: Sendable {
    /// `SystemLanguageModel.contextSize` — the whole window, in tokens.
    public var contextSize: Int
    /// Held back for the model's answer.
    public var reservedForOutput: Int
    /// Held back for the person's question and the tool results that come back.
    public var reservedForWorkingSet: Int
    /// Never attach more than this many tools regardless of budget; a long tool list
    /// degrades selection quality on a small model even when it fits.
    public var maximumTools: Int

    public init(
        contextSize: Int,
        reservedForOutput: Int = 512,
        reservedForWorkingSet: Int = 768,
        maximumTools: Int = 4
    ) {
        self.contextSize = contextSize
        self.reservedForOutput = reservedForOutput
        self.reservedForWorkingSet = reservedForWorkingSet
        self.maximumTools = maximumTools
    }

    /// Tokens available for tool schemas once instructions and reservations are taken.
    public func availableForTools(instructionsCost: Int) -> Int {
        contextSize - reservedForOutput - reservedForWorkingSet - instructionsCost
    }

    /// The text whose token cost stands in for a tool's cost in the window: the model
    /// is shown the name, the description, and the schema.
    public static func billableText(for tool: MCPToolDefinition) -> String {
        [tool.name, tool.title ?? "", tool.description ?? "", tool.inputSchema.jsonString]
            .filter { !$0.isEmpty }
            .joined(separator: " ")
    }
}

/// Picks the tools to attach for one request.
public struct ToolSelector: Sendable {
    public var relevance: ToolRelevance
    public var budget: ToolBudget

    public init(budget: ToolBudget, relevance: ToolRelevance = ToolRelevance()) {
        self.budget = budget
        self.relevance = relevance
    }

    public struct Selection: Sendable, Equatable {
        public var tools: [MCPToolDefinition]
        public var tokensUsed: Int
        public var tokensAvailable: Int
        /// Tools that scored above zero but did not fit. Worth surfacing: it means the
        /// person's question may need a narrower allowlist or a bigger window.
        public var dropped: [String]
    }

    /// Ranks by relevance, then admits greedily while the schema cost fits.
    /// `measure` is the model's tokenizer; inject `model.tokenCount(for:)`.
    public func select(
        query: String,
        from tools: [MCPToolDefinition],
        instructionsCost: Int,
        measure: (String) -> Int
    ) -> Selection {
        let available = budget.availableForTools(instructionsCost: instructionsCost)
        guard available > 0 else {
            return Selection(tools: [], tokensUsed: 0, tokensAvailable: max(0, available), dropped: tools.map(\.name))
        }

        let ranked = tools
            .map { (tool: $0, score: relevance.score(query: query, tool: $0)) }
            .filter { $0.score > 0 }
            .sorted {
                $0.score == $1.score ? $0.tool.name < $1.tool.name : $0.score > $1.score
            }

        var chosen: [MCPToolDefinition] = []
        var dropped: [String] = []
        var used = 0

        for candidate in ranked {
            guard chosen.count < budget.maximumTools else {
                dropped.append(candidate.tool.name)
                continue
            }
            let cost = measure(ToolBudget.billableText(for: candidate.tool))
            if used + cost <= available {
                chosen.append(candidate.tool)
                used += cost
            } else {
                dropped.append(candidate.tool.name)
            }
        }

        // Tools that matched nothing are not "dropped" — they were never candidates.
        return Selection(tools: chosen, tokensUsed: used, tokensAvailable: available, dropped: dropped)
    }
}
