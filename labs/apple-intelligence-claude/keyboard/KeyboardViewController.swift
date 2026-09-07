import UIKit
import OnDeviceAssistant

// A keyboard extension is the only surface that answers a question without leaving
// the app you are typing in. It is also the most constrained process on the system:
// roughly 30-60 MB before jetsam kills it with no crash log, and no network at all
// unless the person grants Allow Full Access.
//
// So this controller stays thin. It owns a button, a status label, and the text
// handoff. All reasoning happens in OnDeviceAssistant, and the keyboard never
// links a cloud model package.
//
// Add to the extension target's Info.plist, under NSExtensionAttributes:
//   RequestsOpenAccess = true          (required for the gateway network calls)
//   IsASCIICapable      = false
//   PrimaryLanguage     = en-US
//
// See docs/06-typing-surfaces.md for what Full Access means for the person's privacy.

public final class KeyboardViewController: UIInputViewController {

    private let askButton = UIButton(type: .system)
    private let statusLabel = UILabel()
    private let nextKeyboardButton = UIButton(type: .system)
    private var work: Task<Void, Never>?

    // The question is taken from what precedes the cursor. The system truncates
    // documentContextBeforeInput, so cap it and never delete more than we captured.
    private static let maximumQuestionLength = 400

    public override func viewDidLoad() {
        super.viewDidLoad()
        configureViews()
        refreshReadiness()
    }

    public override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        work?.cancel()
    }

    public override func textDidChange(_ textInput: (any UITextInput)?) {
        super.textDidChange(textInput)
        askButton.isEnabled = work == nil && !capturedQuestion().isEmpty
    }

    // MARK: Actions

    @objc private func ask() {
        guard work == nil else { return }

        let question = capturedQuestion()
        guard !question.isEmpty else { return }

        if !hasFullAccess {
            // Without Full Access the gateway is unreachable. The model still answers
            // from what it knows, so say what is degraded rather than failing silently.
            show("No Full Access — answering without your tools.")
        } else {
            show("Thinking…")
        }
        askButton.isEnabled = false

        work = Task { [weak self] in
            guard let self else { return }
            let answer: String
            do {
                answer = try await OnDeviceAssistant.shared.answer(question)
            } catch let failure as OnDeviceFailure {
                answer = AskInlineIntent.message(for: failure)
            } catch {
                answer = "Could not answer that."
            }

            await MainActor.run {
                guard !Task.isCancelled else { return }
                self.replace(question: question, with: answer)
                self.work = nil
                self.refreshReadiness()
            }
        }
    }

    @objc private func advanceToNext() {
        advanceToNextInputMode()
    }

    // MARK: Text handoff

    /// The text the person typed since the last sentence break, treated as the question.
    private func capturedQuestion() -> String {
        let before = textDocumentProxy.documentContextBeforeInput ?? ""
        guard !before.isEmpty else { return "" }

        // Start after the last hard break so the assistant does not swallow the
        // paragraph the person was already writing.
        let breaks = CharacterSet(charactersIn: "\n")
        let lastLine = before.components(separatedBy: breaks).last ?? before
        let trimmed = lastLine.trimmingCharacters(in: .whitespaces)

        return String(trimmed.suffix(Self.maximumQuestionLength))
    }

    /// Removes exactly the captured question and inserts the answer in its place.
    private func replace(question: String, with answer: String) {
        // Only delete what is still there: the person may have kept typing.
        let before = textDocumentProxy.documentContextBeforeInput ?? ""
        if before.hasSuffix(question) {
            for _ in 0..<question.count {
                textDocumentProxy.deleteBackward()
            }
        }
        textDocumentProxy.insertText(answer)
    }

    // MARK: Views

    private func configureViews() {
        view.backgroundColor = UIColor.secondarySystemBackground

        askButton.setTitle("Ask", for: .normal)
        askButton.titleLabel?.font = .preferredFont(forTextStyle: .headline)
        askButton.accessibilityHint = "Answers the question you just typed and replaces it with the answer"
        askButton.addTarget(self, action: #selector(ask), for: .touchUpInside)

        nextKeyboardButton.setTitle("􀆪", for: .normal)
        nextKeyboardButton.accessibilityLabel = "Next keyboard"
        nextKeyboardButton.addTarget(self, action: #selector(advanceToNext), for: .touchUpInside)

        statusLabel.font = .preferredFont(forTextStyle: .footnote)
        statusLabel.textColor = .secondaryLabel
        statusLabel.adjustsFontSizeToFitWidth = true
        statusLabel.numberOfLines = 1

        let row = UIStackView(arrangedSubviews: [nextKeyboardButton, statusLabel, askButton])
        row.axis = .horizontal
        row.alignment = .center
        row.spacing = 12
        row.isLayoutMarginsRelativeArrangement = true
        row.directionalLayoutMargins = .init(top: 10, leading: 14, bottom: 10, trailing: 14)
        row.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(row)

        NSLayoutConstraint.activate([
            row.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            row.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            row.topAnchor.constraint(equalTo: view.topAnchor),
            view.heightAnchor.constraint(equalToConstant: 56),
        ])
    }

    private func refreshReadiness() {
        if let problem = OnDeviceAssistant.availabilityProblem() {
            show(problem)
            askButton.isEnabled = false
            return
        }
        show(hasFullAccess ? "Type a question, then tap Ask." : "Allow Full Access to reach your tools.")
        askButton.isEnabled = !capturedQuestion().isEmpty
    }

    private func show(_ text: String) {
        statusLabel.text = text
    }
}
