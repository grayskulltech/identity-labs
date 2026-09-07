import FoundationModels

/// Structured output for a sign-in triage. The model fills this via guided
/// generation, so the app never parses free text to reach a decision.
@Generable
public struct TriageVerdict: Sendable {
    @Generable
    public enum RiskLevel: Sendable {
        case low
        case medium
        case high
        case critical
    }

    @Generable
    public enum RecommendedAction: Sendable {
        case noAction
        case notifyUser
        case requireStepUpMFA
        case revokeSessions
        case disableAccount
    }

    @Guide(description: "Overall risk of the sign-in activity under review")
    public var risk: RiskLevel

    @Guide(description: "One action the analyst should take next")
    public var action: RecommendedAction

    @Guide(description: "The specific signals that drove the verdict, as short phrases", .count(1...6))
    public var signals: [String]

    @Guide(description: "Two to three sentences an analyst can paste into a ticket")
    public var summary: String
}
