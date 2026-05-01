import SwiftUI

struct NewCaseSheet: View {
    @EnvironmentObject var api: APIClient
    @Environment(\.dismiss) private var dismiss
    var onCreated: () async -> Void

    @State private var clientName = ""
    @State private var matterType = "Civil Litigation"
    @State private var initialMsg = ""
    @State private var busy = false
    @State private var errorMsg: String?

    private let matterOptions = [
        "Civil Litigation",
        "Commercial Lease Dispute",
        "Family Law",
        "Criminal Defense",
        "Immigration",
        "Intellectual Property",
        "Other"
    ]

    private var canCreate: Bool {
        !busy && !clientName.trimmingCharacters(in: .whitespaces).isEmpty
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Client") {
                    TextField("Client name", text: $clientName, prompt: Text("e.g. Acme Corp."))
                    Picker("Matter type", selection: $matterType) {
                        ForEach(matterOptions, id: \.self) { Text($0).tag($0) }
                    }
                    .pickerStyle(.menu)
                }

                Section {
                    TextEditor(text: $initialMsg)
                        .frame(minHeight: 120)
                        .overlay(alignment: .topLeading) {
                            if initialMsg.isEmpty {
                                Text("What's the matter? A few sentences help the AI draft an intake evaluation.")
                                    .foregroundStyle(.secondary)
                                    .padding(.top, 8)
                                    .padding(.leading, 4)
                                    .allowsHitTesting(false)
                            }
                        }
                } header: {
                    Text("Initial message")
                } footer: {
                    Text("Optional. The AI uses this to pre-fill a triage evaluation.")
                }

                if let e = errorMsg {
                    Section {
                        Label(e, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                            .font(.callout)
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle("New case")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .disabled(busy)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        Task { await create() }
                    } label: {
                        if busy {
                            HStack(spacing: 6) {
                                ProgressView().controlSize(.small)
                                Text("Creating…")
                            }
                        } else {
                            Text("Create")
                        }
                    }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!canCreate)
                }
            }
        }
        .frame(minWidth: 480, minHeight: 440)
    }

    private func create() async {
        busy = true; defer { busy = false }
        errorMsg = nil
        do {
            try await api.createCase(
                clientName: clientName.trimmingCharacters(in: .whitespaces),
                matterType: matterType,
                initialMsg: initialMsg.trimmingCharacters(in: .whitespacesAndNewlines)
            )
            await onCreated()
            dismiss()
        } catch {
            errorMsg = error.localizedDescription
        }
    }
}
