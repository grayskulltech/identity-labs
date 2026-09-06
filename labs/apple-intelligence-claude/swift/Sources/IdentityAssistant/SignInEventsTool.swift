import Foundation
import FoundationModels

/// A sign-in record as the tool returns it to the model. Keep this minimal: every
/// field here is sent to whichever model is serving the session.
public struct SignInEvent: Sendable, Codable, Equatable {
    public var timestamp: Date
    public var subject: String
    public var sourceCountry: String
    public var clientApp: String
    public var mfaResult: String
    public var outcome: String

    public init(timestamp: Date, subject: String, sourceCountry: String, clientApp: String, mfaResult: String, outcome: String) {
        self.timestamp = timestamp
        self.subject = subject
        self.sourceCountry = sourceCountry
        self.clientApp = clientApp
        self.mfaResult = mfaResult
        self.outcome = outcome
    }
}

/// Abstracts the identity provider. The lab ships an in-memory implementation;
/// production wires this to the IdP's audit log API.
public protocol SignInEventSource: Sendable {
    func recentEvents(for subject: String, limit: Int) async throws -> [SignInEvent]
}

/// Client-side tool. The framework invokes `call(arguments:)` on the device when the
/// model requests it, on either the on-device model or Claude. Data leaves the device
/// only as part of the tool output that the framework appends to the transcript.
public struct SignInEventsTool: Tool {
    public let name = "recentSignInEvents"
    public let description = "Fetches the most recent sign-in events for a user, newest first. Use it before judging risk."

    @Generable
    public struct Arguments: Sendable {
        @Guide(description: "The user identifier exactly as it appears in the analyst's request")
        public var subject: String

        @Guide(description: "How many events to return", .range(1...25))
        public var limit: Int
    }

    private let source: any SignInEventSource
    private let redactor: SubjectRedactor

    public init(source: any SignInEventSource, redactor: SubjectRedactor = SubjectRedactor()) {
        self.source = source
        self.redactor = redactor
    }

    public func call(arguments: Arguments) async throws -> String {
        let events = try await source.recentEvents(for: arguments.subject, limit: arguments.limit)
        let formatter = ISO8601DateFormatter()
        let lines = events.map { event in
            [
                formatter.string(from: event.timestamp),
                redactor.redact(event.subject),
                event.sourceCountry,
                event.clientApp,
                "mfa=\(event.mfaResult)",
                "outcome=\(event.outcome)",
            ].joined(separator: " | ")
        }
        return lines.isEmpty ? "No sign-in events found." : lines.joined(separator: "\n")
    }
}

/// Replaces the subject with a stable pseudonym before it enters the transcript.
/// The model reasons about "user-1a2b" and the analyst maps it back locally.
public struct SubjectRedactor: Sendable {
    public init() {}

    public func redact(_ subject: String) -> String {
        var hash: UInt32 = 2_166_136_261
        for byte in subject.utf8 {
            hash ^= UInt32(byte)
            hash = hash &* 16_777_619
        }
        return "user-" + String(hash, radix: 16)
    }
}

/// In-memory source for the lab and for unit tests.
public struct StaticSignInEventSource: SignInEventSource {
    private let events: [SignInEvent]

    public init(events: [SignInEvent]) {
        self.events = events
    }

    public func recentEvents(for subject: String, limit: Int) async throws -> [SignInEvent] {
        Array(
            events
                .filter { $0.subject.caseInsensitiveCompare(subject) == .orderedSame }
                .sorted { $0.timestamp > $1.timestamp }
                .prefix(limit)
        )
    }
}
