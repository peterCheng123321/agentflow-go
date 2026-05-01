import SwiftUI

// MARK: - Local stubs (parallel-unit safe)
//
// Unit 4 owns the canonical `DocumentInfo` struct in `Models.swift` and Unit 5
// owns `DocCategory`. Until those land, this file declares minimal local stubs
// guarded by `canImport` flags so the integration unit (Unit 12) can drop them
// without touching call sites.
//
// TODO(integration): drop these stubs once Unit 4 lands.
// TODO(integration): drop these stubs once Unit 5 lands.

#if !canImport(AgentFlowSharedDocumentInfoStub)
struct DocumentInfo: Codable, Identifiable, Hashable {
    var id: String { filename }
    let filename: String
    let doctype: String?
    let ocr_indexed: Bool?
    let rag_indexed: Bool?
    let size_bytes: Int64?
    let modified_at: Date?
}
#endif

#if !canImport(AgentFlowSharedDocCategoryStub)
enum DocCategory: String, CaseIterable, Hashable {
    case pleadings
    case evidence
    case other

    var displayLabel: String {
        switch self {
        case .pleadings: return "Pleadings"
        case .evidence:  return "Evidence"
        case .other:     return "Other"
        }
    }

    var symbol: String {
        switch self {
        case .pleadings: return "doc.richtext.fill"
        case .evidence:  return "photo.on.rectangle.angled"
        case .other:     return "doc.fill"
        }
    }

    static func classify(filename: String, doctype: String?) -> DocCategory {
        // Minimal local stub — Unit 5 owns the real classifier.
        return .other
    }
}
#endif

// MARK: - Filename + icon helpers
//
// Mirror of the `cleanFilename` / `iconForDoc` helpers in CaseHubView so this
// view stays free-standing. Unit 12 may collapse the duplicates.

private func dt_fileExt(_ s: String) -> String {
    (s as NSString).pathExtension
}

private func dt_cleanFilename(_ s: String) -> String {
    let ext = dt_fileExt(s)
    guard !ext.isEmpty else { return s }
    let withoutExt = (s as NSString).deletingPathExtension
    if dt_fileExt(withoutExt).lowercased() == ext.lowercased() {
        return withoutExt
    }
    return s
}

private func dt_iconForDoc(_ name: String) -> String {
    switch dt_fileExt(name).lowercased() {
    case "pdf": return "doc.richtext.fill"
    case "png", "jpg", "jpeg", "heic": return "photo.fill"
    case "docx", "doc": return "doc.text.fill"
    case "zip": return "archivebox.fill"
    case "txt", "md": return "text.alignleft"
    default: return "doc.fill"
    }
}

// MARK: - DocumentTile

/// SwiftUI view that renders ONE document either as a tile (grid mode) or a
/// row (list mode). The view is intentionally state-less — selection is owned
/// by the parent and surfaced via `isSelected`; taps are routed back through
/// `onOpen` and `onSelectToggle`.
struct DocumentTile: View {
    enum Style { case tile, row }

    let info: DocumentInfo
    let style: Style
    let isSelected: Bool
    let onOpen: () -> Void
    let onSelectToggle: () -> Void
    /// Built by the parent via `APIClient.documentThumbnailURL`; `nil` means
    /// "fall back to the category icon".
    let thumbnailURL: URL?

    var body: some View {
        switch style {
        case .tile: tileBody
        case .row:  rowBody
        }
    }

    // MARK: - Tile mode

    private var tileBody: some View {
        VStack(alignment: .leading, spacing: AF.Space.xs) {
            thumbnail(size: CGSize(width: 120, height: 80))
                .frame(maxWidth: .infinity)

            Text(dt_cleanFilename(info.filename))
                .font(.callout)
                .lineLimit(2)
                .multilineTextAlignment(.leading)
                .frame(maxWidth: .infinity, alignment: .leading)
                .fixedSize(horizontal: false, vertical: true)

            footerChips
        }
        .padding(AF.Space.s)
        .frame(width: 140, height: 160, alignment: .top)
        .background(
            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                .fill(isSelected
                      ? Color.accentColor.opacity(0.18)
                      : Color.clear)
        )
        .overlay(
            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                .stroke(isSelected ? Color.accentColor : Color.clear,
                        lineWidth: 1.5)
        )
        .contentShape(Rectangle())
        .onTapGesture { onOpen() }
        .simultaneousGesture(
            TapGesture().modifiers(.command).onEnded { onSelectToggle() }
        )
        .accessibilityElement(children: .combine)
        .accessibilityLabel(Text(dt_cleanFilename(info.filename)))
        .accessibilityAddTraits(isSelected ? [.isSelected] : [])
    }

    // MARK: - Row mode

    private var rowBody: some View {
        HStack(spacing: AF.Space.s) {
            thumbnail(size: CGSize(width: 24, height: 24))
                .frame(width: 24, height: 24)

            Text(dt_cleanFilename(info.filename))
                .font(.callout)
                .lineLimit(1)
                .truncationMode(.middle)
                .frame(maxWidth: .infinity, alignment: .leading)

            categoryChip
            statusChips
        }
        .padding(.horizontal, AF.Space.s)
        .frame(height: 44)
        .background(
            RoundedRectangle(cornerRadius: AF.Radius.s, style: .continuous)
                .fill(isSelected
                      ? Color.accentColor.opacity(0.18)
                      : Color.clear)
        )
        .contentShape(Rectangle())
        .onTapGesture { onOpen() }
        .simultaneousGesture(
            TapGesture().modifiers(.command).onEnded { onSelectToggle() }
        )
        .accessibilityElement(children: .combine)
        .accessibilityLabel(Text(dt_cleanFilename(info.filename)))
        .accessibilityAddTraits(isSelected ? [.isSelected] : [])
    }

    // MARK: - Subviews

    @ViewBuilder
    private func thumbnail(size: CGSize) -> some View {
        let category = DocCategory.classify(filename: info.filename,
                                            doctype: info.doctype)
        let iconName = dt_iconForDoc(info.filename)

        ZStack {
            RoundedRectangle(cornerRadius: AF.Radius.s, style: .continuous)
                .fill(Color.secondary.opacity(0.12))

            if let url = thumbnailURL {
                AsyncImage(url: url) { phase in
                    switch phase {
                    case .success(let image):
                        image
                            .resizable()
                            .scaledToFill()
                            .clipShape(RoundedRectangle(cornerRadius: AF.Radius.s,
                                                        style: .continuous))
                    case .empty, .failure:
                        Image(systemName: category == .other ? iconName : category.symbol)
                            .font(.system(size: max(16, size.width * 0.32),
                                          weight: .regular))
                            .foregroundStyle(.secondary)
                    @unknown default:
                        Image(systemName: iconName)
                            .font(.system(size: max(16, size.width * 0.32),
                                          weight: .regular))
                            .foregroundStyle(.secondary)
                    }
                }
            } else {
                Image(systemName: category == .other ? iconName : category.symbol)
                    .font(.system(size: max(16, size.width * 0.32),
                                  weight: .regular))
                    .foregroundStyle(.secondary)
            }
        }
        .frame(width: size.width, height: size.height)
        .clipShape(RoundedRectangle(cornerRadius: AF.Radius.s, style: .continuous))
    }

    private var footerChips: some View {
        HStack(spacing: AF.Space.xxs) {
            categoryChip
            Spacer(minLength: 0)
            statusChips
        }
    }

    private var categoryChip: some View {
        let label: String = {
            if let dt = info.doctype, !dt.isEmpty { return dt }
            return DocCategory.classify(filename: info.filename,
                                        doctype: info.doctype).displayLabel
        }()
        return Text(label)
            .font(.caption2.weight(.semibold))
            .lineLimit(1)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(Capsule().fill(.ultraThinMaterial))
            .foregroundStyle(.secondary)
    }

    @ViewBuilder
    private var statusChips: some View {
        HStack(spacing: 4) {
            if info.ocr_indexed == true {
                badge("OCR")
            }
            if info.rag_indexed == true {
                badge("RAG")
            }
        }
    }

    private func badge(_ label: String) -> some View {
        HStack(spacing: 2) {
            Text(label)
            Image(systemName: "checkmark")
        }
        .font(.caption2.weight(.semibold))
        .padding(.horizontal, 5)
        .padding(.vertical, 2)
        .background(
            Capsule().fill(AF.Palette.tint(.green).opacity(0.18))
        )
        .foregroundStyle(AF.Palette.tint(.green))
    }
}

// MARK: - Previews

#Preview("Tile · loaded") {
    DocumentTile(
        info: DocumentInfo(
            filename: "complaint_v2.pdf",
            doctype: "Complaint",
            ocr_indexed: true,
            rag_indexed: true,
            size_bytes: 482_133,
            modified_at: Date()
        ),
        style: .tile,
        isSelected: false,
        onOpen: {},
        onSelectToggle: {},
        thumbnailURL: nil
    )
    .padding()
}

#Preview("Tile · selected") {
    DocumentTile(
        info: DocumentInfo(
            filename: "exhibit_a.png",
            doctype: "Exhibit",
            ocr_indexed: true,
            rag_indexed: false,
            size_bytes: 91_022,
            modified_at: Date()
        ),
        style: .tile,
        isSelected: true,
        onOpen: {},
        onSelectToggle: {},
        thumbnailURL: nil
    )
    .padding()
}

#Preview("Row") {
    VStack(spacing: 4) {
        DocumentTile(
            info: DocumentInfo(
                filename: "engagement_letter.docx",
                doctype: "Letter",
                ocr_indexed: false,
                rag_indexed: true,
                size_bytes: 22_400,
                modified_at: Date()
            ),
            style: .row,
            isSelected: false,
            onOpen: {},
            onSelectToggle: {},
            thumbnailURL: nil
        )
        DocumentTile(
            info: DocumentInfo(
                filename: "scan_001.jpg",
                doctype: nil,
                ocr_indexed: nil,
                rag_indexed: nil,
                size_bytes: 1_204_882,
                modified_at: Date()
            ),
            style: .row,
            isSelected: true,
            onOpen: {},
            onSelectToggle: {},
            thumbnailURL: nil
        )
    }
    .frame(width: 480)
    .padding()
}
