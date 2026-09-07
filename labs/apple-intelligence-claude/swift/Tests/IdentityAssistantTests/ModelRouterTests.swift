import Testing
import Foundation
@testable import IdentityAssistant

// Routing and classification are pure functions and run without a network or an
// Anthropic account. Session construction still needs an OS 27 host.

@Suite struct PIIClassifierTests {
    let classifier = PIIClassifier()

    @Test func detectsEmailAddress() {
        #expect(classifier.signals(in: "review gtownsend@example.com").contains(.emailAddress))
    }

    @Test func detectsIPv4() {
        #expect(classifier.signals(in: "source 203.0.113.42 looks new").contains(.ipv4Address))
    }

    @Test func detectsBearerToken() {
        let jwtLike = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhYmMifQ.sig"
        #expect(classifier.signals(in: jwtLike).contains(.bearerToken))
    }

    @Test func detectsDownLevelLogonName() {
        #expect(classifier.signals(in: #"CORP\gtownsend failed MFA"#).contains(.userPrincipalName))
    }

    @Test func cleanTextHasNoSignals() {
        #expect(classifier.signals(in: "Summarize the difference between OIDC and SAML.").isEmpty)
    }
}

@Suite struct SubjectRedactorTests {
    @Test func redactionIsStableAndNotReversibleByInspection() {
        let redactor = SubjectRedactor()
        let a = redactor.redact("gtownsend@example.com")
        let b = redactor.redact("gtownsend@example.com")
        #expect(a == b)
        #expect(a.hasPrefix("user-"))
        #expect(!a.contains("example"))
    }
}

@Suite struct StaticSignInEventSourceTests {
    @Test func returnsNewestFirstAndRespectsLimit() async throws {
        let now = Date()
        let source = StaticSignInEventSource(events: [
            SignInEvent(timestamp: now.addingTimeInterval(-300), subject: "a@example.com", sourceCountry: "US", clientApp: "Outlook", mfaResult: "success", outcome: "success"),
            SignInEvent(timestamp: now, subject: "a@example.com", sourceCountry: "RU", clientApp: "Legacy IMAP", mfaResult: "none", outcome: "success"),
            SignInEvent(timestamp: now.addingTimeInterval(-60), subject: "b@example.com", sourceCountry: "US", clientApp: "Teams", mfaResult: "success", outcome: "success"),
        ])
        let events = try await source.recentEvents(for: "A@example.com", limit: 1)
        #expect(events.count == 1)
        #expect(events.first?.sourceCountry == "RU")
    }
}
