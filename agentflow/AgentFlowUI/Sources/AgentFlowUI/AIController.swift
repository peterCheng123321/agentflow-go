import Foundation
import SwiftUI

/// Owns chat state and model selection. Lives at the App root so conversations
/// survive case-selection changes and sheet transitions.
@MainActor
final class AIController: ObservableObject {
    @Published var messages: [APIClient.ChatMessage] = []
    @Published var models: [APIClient.LLMModel] = []
    @AppStorage("af.model") var selectedModelID: String = ""
    @Published var isLoading: Bool = false
    @Published var contextCaseID: String?
    @Published var lastSources: [String] = []
    @Published var lastError: String?
    @Published var useRAG: Bool = true

    var selectedModel: APIClient.LLMModel? {
        models.first { $0.id == selectedModelID } ?? models.first { ($0.is_default ?? false) } ?? models.first
    }

    /// Quick-action prompts surfaced in the inspector.
    enum QuickAction: String, CaseIterable, Identifiable {
        case summarize, entities, draft, similar, timeline
        var id: String { rawValue }
        var title: String {
            switch self {
            case .summarize: return "Summarize case"
            case .entities:  return "Extract entities"
            case .draft:     return "Draft filing"
            case .similar:   return "Find similar cases"
            case .timeline:  return "Build timeline"
            }
        }
        var icon: String {
            switch self {
            case .summarize: return "text.redaction"
            case .entities:  return "person.2.crop.square.stack"
            case .draft:     return "doc.text.magnifyingglass"
            case .similar:   return "square.grid.3x3.square"
            case .timeline:  return "calendar.day.timeline.left"
            }
        }
        var prompt: String {
            switch self {
            case .summarize: return "Summarize this case: parties, dispute, current state, next step."
            case .entities:  return "List all people, companies, dates, amounts, and case numbers mentioned."
            case .draft:     return "Draft the next filing for this case based on available documents."
            case .similar:   return "Find similar cases in the knowledge base and cite them."
            case .timeline:  return "Reconstruct a chronological timeline of events from the evidence."
            }
        }
    }

    func loadModelsIfNeeded(api: APIClient) async {
        guard models.isEmpty else { return }
        do {
            let resp = try await api.listModels()
            models = resp.models
            if selectedModelID.isEmpty {
                selectedModelID = resp.current ?? resp.models.first?.id ?? ""
            }
        } catch {
            lastError = "Could not load models: \(error.localizedDescription)"
        }
    }

    func bind(toCase caseID: String?) {
        if contextCaseID != caseID {
            contextCaseID = caseID
            messages.removeAll()
            lastSources.removeAll()
            lastError = nil
        }
    }

    func run(_ action: QuickAction, api: APIClient) {
        send(action.prompt, api: api)
    }

    func send(_ text: String, api: APIClient) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        let userMsg = APIClient.ChatMessage(role: "user", content: trimmed)
        messages.append(userMsg)
        isLoading = true
        lastError = nil
        let snapshot = messages
        let caseID = contextCaseID
        let model = selectedModelID.isEmpty ? nil : selectedModelID
        let useRAG = self.useRAG
        Task { [weak self] in
            do {
                let resp = try await api.chat(messages: snapshot, caseID: caseID, useRAG: useRAG, model: model)
                await MainActor.run {
                    guard let self else { return }
                    self.messages.append(.init(role: "assistant", content: resp.reply))
                    self.lastSources = resp.sources ?? []
                    self.isLoading = false
                }
            } catch {
                await MainActor.run {
                    guard let self else { return }
                    self.lastError = error.localizedDescription
                    self.isLoading = false
                }
            }
        }
    }

    func reset() {
        messages.removeAll()
        lastSources.removeAll()
        lastError = nil
    }
}
