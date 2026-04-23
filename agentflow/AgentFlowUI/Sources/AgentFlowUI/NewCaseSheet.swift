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

    var body: some View {
        VStack(alignment: .leading, spacing: AF.Space.m) {
            HStack {
                Text("New case").font(.title2.weight(.semibold))
                Spacer()
                Button {
                    dismiss()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }

            VStack(alignment: .leading, spacing: 6) {
                Text("CLIENT NAME").font(.caption2).foregroundStyle(.secondary).tracking(0.6)
                TextField("e.g. Acme Corp.", text: $clientName)
                    .textFieldStyle(.plain)
                    .padding(10)
                    .background(
                        RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                            .fill(Color.black.opacity(0.22))
                    )
            }

            VStack(alignment: .leading, spacing: 6) {
                Text("MATTER TYPE").font(.caption2).foregroundStyle(.secondary).tracking(0.6)
                Picker("", selection: $matterType) {
                    ForEach(matterOptions, id: \.self) { Text($0).tag($0) }
                }
                .labelsHidden()
                .pickerStyle(.menu)
                .padding(6)
                .background(
                    RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                        .fill(Color.black.opacity(0.22))
                )
            }

            VStack(alignment: .leading, spacing: 6) {
                Text("INITIAL MESSAGE").font(.caption2).foregroundStyle(.secondary).tracking(0.6)
                TextEditor(text: $initialMsg)
                    .font(.callout)
                    .frame(minHeight: 90)
                    .scrollContentBackground(.hidden)
                    .padding(8)
                    .background(
                        RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                            .fill(Color.black.opacity(0.22))
                    )
            }

            if let e = errorMsg {
                Text(e).foregroundStyle(.red).font(.caption)
            }

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .buttonStyle(.afGhost)
                    .disabled(busy)
                Button {
                    Task { await create() }
                } label: {
                    if busy {
                        HStack { ProgressView().controlSize(.small); Text("Creating…") }
                    } else {
                        Text("Create case")
                    }
                }
                .buttonStyle(.afPrimary)
                .disabled(busy || clientName.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(AF.Space.l)
        .frame(width: 460)
        .background(
            RoundedRectangle(cornerRadius: AF.Radius.xl, style: .continuous)
                .fill(.ultraThinMaterial)
        )
        .overlay(
            RoundedRectangle(cornerRadius: AF.Radius.xl, style: .continuous)
                .strokeBorder(.white.opacity(0.1), lineWidth: 1)
        )
    }

    private func create() async {
        busy = true; defer { busy = false }
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
