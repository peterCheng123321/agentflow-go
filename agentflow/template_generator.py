import asyncio
import hashlib
import json
import os
import re
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from datetime import datetime, UTC
from typing import Any


PREFERENCES_PATH = os.path.join(os.path.dirname(__file__), "data", "template_preferences.json")
os.makedirs(os.path.dirname(PREFERENCES_PATH), exist_ok=True)

DOCUMENT_TYPES = {
    "lease_dispute_letter": {
        "id": "lease_dispute_letter",
        "label": "Lease Dispute Letter (租金纠纷函)",
        "description": "Formal letter to landlord/tenant regarding lease dispute",
        "matter_types": ["Commercial Lease Dispute", "Residential Lease Dispute"],
        "fields": ["client_name", "opposing_party", "dispute_summary", "desired_outcome", "deadline_date"],
        "system_prompt": "请起草一份关于{dispute_summary}的律师函，当事人为{client_name}，对方为{opposing_party}，期望结果：{desired_outcome}。",
    },
    "evidence_list": {
        "id": "evidence_list",
        "label": "Evidence List (证据清单)",
        "description": "Structured list of evidence for court submission",
        "matter_types": ["Commercial Lease Dispute", "Civil Litigation"],
        "fields": ["client_name", "case_description", "evidence_items"],
        "system_prompt": "请为案件{case_description}整理一份证据清单，包含{evidence_items}，当事人：{client_name}。",
    },
    "case_summary": {
        "id": "case_summary",
        "label": "Case Summary (案件摘要)",
        "description": "Executive summary of case facts and legal issues",
        "matter_types": ["Commercial Lease Dispute", "Civil Litigation", "Contract Review"],
        "fields": ["client_name", "matter_type", "key_facts", "legal_issues", "recommended_action"],
        "system_prompt": "请为当事人{client_name}的{matter_type}案件撰写一份案件摘要。关键事实：{key_facts}。法律问题：{legal_issues}。建议行动：{recommended_action}。",
    },
    "settlement_agreement": {
        "id": "settlement_agreement",
        "label": "Settlement Agreement Draft (和解协议草稿)",
        "description": "Draft settlement agreement for negotiation",
        "matter_types": ["Commercial Lease Dispute", "Civil Litigation"],
        "fields": ["client_name", "opposing_party", "settlement_terms", "payment_schedule", "effective_date"],
        "system_prompt": "请起草一份和解协议，当事人{client_name}与{opposing_party}，和解条款：{settlement_terms}，付款计划：{payment_schedule}，生效日期：{effective_date}。",
    },
    "court_filing": {
        "id": "court_filing",
        "label": "Court Filing Draft (起诉状草稿)",
        "description": "Draft civil complaint for court submission",
        "matter_types": ["Commercial Lease Dispute", "Civil Litigation"],
        "fields": ["client_name", "defendant_name", "claim_amount", "legal_basis", "facts_narrative"],
        "system_prompt": "请起草一份民事起诉状，原告{client_name}，被告{defendant_name}，诉讼请求金额：{claim_amount}，法律依据：{legal_basis}，事实陈述：{facts_narrative}。",
    },
    "client_intake_form": {
        "id": "client_intake_form",
        "label": "Client Intake Form (客户接案表)",
        "description": "Standard intake form for new client cases",
        "matter_types": ["Commercial Lease Dispute", "Civil Litigation", "Contract Review"],
        "fields": ["client_name", "contact_phone", "contact_email", "matter_type", "brief_description", "estimated_value"],
        "system_prompt": "请根据以下信息生成客户接案表：客户姓名：{client_name}，联系方式：{contact_phone}，{contact_email}，案件类型：{matter_type}，简要描述：{brief_description}，预估价值：{estimated_value}。",
    },
    "progress_report": {
        "id": "progress_report",
        "label": "Client Progress Report (进展报告)",
        "description": "Periodic status update for client",
        "matter_types": ["Commercial Lease Dispute", "Civil Litigation"],
        "fields": ["client_name", "case_id", "current_status", "recent_developments", "next_steps", "estimated_completion"],
        "system_prompt": "请为案件编号{case_id}的当事人{client_name}撰写进展报告。当前状态：{current_status}，近期进展：{recent_developments}，下一步：{next_steps}，预计完成：{estimated_completion}。",
    },
}


@dataclass
class TemplateDraft:
    template_id: str
    template_label: str
    filled_fields: dict[str, str]
    generated_text: str
    research_results: list[dict]
    created_at: str
    case_id: str | None = None
    edited_text: str | None = None
    final_adopted: bool = False


@dataclass
class UserPreferences:
    default_template_id: str = "case_summary"
    default_matter_type: str = "Commercial Lease Dispute"
    default_fields: dict[str, str] = field(default_factory=dict)
    recent_templates: list[str] = field(default_factory=list)
    field_defaults: dict[str, dict[str, str]] = field(default_factory=dict)
    auto_research_enabled: bool = True
    research_depth: int = 3
    drafts: list[dict] = field(default_factory=list)

    def save(self):
        payload = {
            "default_template_id": self.default_template_id,
            "default_matter_type": self.default_matter_type,
            "default_fields": self.default_fields,
            "recent_templates": self.recent_templates[-20:],
            "field_defaults": self.field_defaults,
            "auto_research_enabled": self.auto_research_enabled,
            "research_depth": self.research_depth,
            "drafts": self.drafts[-50:],
        }
        with open(PREFERENCES_PATH, "w", encoding="utf-8") as f:
            json.dump(payload, f, ensure_ascii=False, indent=2)

    @classmethod
    def load(cls) -> "UserPreferences":
        if not os.path.exists(PREFERENCES_PATH):
            return cls()
        try:
            with open(PREFERENCES_PATH, "r", encoding="utf-8") as f:
                data = json.load(f)
            return cls(
                default_template_id=data.get("default_template_id", "case_summary"),
                default_matter_type=data.get("default_matter_type", "Commercial Lease Dispute"),
                default_fields=data.get("default_fields", {}),
                recent_templates=data.get("recent_templates", []),
                field_defaults=data.get("field_defaults", {}),
                auto_research_enabled=data.get("auto_research_enabled", True),
                research_depth=data.get("research_depth", 3),
                drafts=data.get("drafts", []),
            )
        except (json.JSONDecodeError, IOError):
            return cls()


class WebResearcher:
    def __init__(self, depth: int = 3):
        self.depth = depth
        self._cache: dict[str, list[dict]] = {}

    def search(self, query: str) -> list[dict]:
        cache_key = query.strip().lower()
        if cache_key in self._cache:
            return self._cache[cache_key]

        results = []
        headers = {
            "User-Agent": "Mozilla/5.0 (compatible; LegalAgentBot/1.0; +https://legalagent.local)",
        }

        try:
            import urllib.parse
            encoded = urllib.parse.quote(query.encode("utf-8"))
            import urllib.request
            url = f"https://duckduckgo.com/html/?q={encoded}&kl=cn-cn"
            req = urllib.request.Request(url, headers=headers)
            with urllib.request.urlopen(req, timeout=10) as resp:
                html = resp.read().decode("utf-8", errors="ignore")

            snippet_pattern = re.compile(r'<a class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>')
            snippet_pattern2 = re.compile(r'<a class="result__snippet"[^>]*>(.*?)</a>')
            link_pattern = re.compile(r'href="(https?://[^"]*)"')

            matches = snippet_pattern.findall(html)
            for i, (url_match, title) in enumerate(matches[:self.depth * 2]):
                clean_title = re.sub(r'<[^>]+>', '', title).strip()
                clean_url = link_pattern.search(url_match)
                if clean_url:
                    results.append({
                        "title": clean_title,
                        "url": clean_url.group(1),
                        "query": query,
                        "rank": i,
                    })
        except Exception as e:
            results.append({"title": f"Search unavailable: {e}", "url": "", "query": query, "rank": 0})

        self._cache[cache_key] = results
        return results

    async def research_async(self, queries: list[str]) -> list[dict]:
        def _search(q):
            return self.search(q)

        loop = asyncio.get_running_loop()
        with ThreadPoolExecutor(max_workers=min(len(queries), 5)) as executor:
            tasks = [loop.run_in_executor(executor, _search, q) for q in queries]
            results = await asyncio.gather(*tasks)

        flattened = []
        seen_urls = set()
        for group in results:
            for item in group:
                if item["url"] and item["url"] not in seen_urls:
                    flattened.append(item)
                    seen_urls.add(item["url"])
        return flattened[:self.depth * 3]


class TemplateGenerator:
    def __init__(self, llm=None, rag=None):
        self.llm = llm
        self.rag = rag
        self.preferences = UserPreferences.load()
        self.researcher = WebResearcher(depth=self.preferences.research_depth)

    def list_templates(self, matter_type: str | None = None) -> list[dict]:
        templates = []
        for tid, t in DOCUMENT_TYPES.items():
            if matter_type and matter_type not in t["matter_types"] and matter_type != "all":
                continue
            templates.append({
                "id": tid,
                "label": t["label"],
                "description": t["description"],
                "matter_types": t["matter_types"],
                "fields": t["fields"],
                "is_default": tid == self.preferences.default_template_id,
            })
        return templates

    def get_template(self, template_id: str) -> dict | None:
        t = DOCUMENT_TYPES.get(template_id)
        if not t:
            return None
        saved_defaults = self.preferences.field_defaults.get(template_id, {})
        return {
            **t,
            "saved_defaults": saved_defaults,
            "recently_used": template_id in self.preferences.recent_templates[-5:],
        }

    async def generate(
        self,
        template_id: str,
        field_values: dict[str, str],
        case_id: str | None = None,
        do_research: bool | None = None,
    ) -> TemplateDraft:
        template = DOCUMENT_TYPES.get(template_id)
        if not template:
            raise ValueError(f"Unknown template: {template_id}")

        if do_research is None:
            do_research = self.preferences.auto_research_enabled

        research_results = []
        if do_research:
            research_queries = self._build_research_queries(template_id, field_values)
            if research_queries:
                research_results = await self.researcher.research_async(research_queries)

        research_context = self._format_research_context(research_results)

        rag_context = ""
        if self.rag and field_values.get("matter_type"):
            query = field_values.get("dispute_summary", "") or field_values.get("case_description", "") or field_values.get("brief_description", "")
            if query:
                rag_hits = self.rag.search_structured(query, k=3)
                rag_context = "\n\n".join(
                    f"[{h['filename']}] {h['chunk']}" for h in rag_hits[:2] if h.get("chunk")
                )

        system_prompt = template["system_prompt"]
        filled_prompt = system_prompt
        for field_name, field_value in field_values.items():
            placeholder = f"{{{field_name}}}"
            filled_prompt = filled_prompt.replace(placeholder, field_value)

        context_parts = []
        if research_context:
            context_parts.append(f"网络检索结果:\n{research_context}")
        if rag_context:
            context_parts.append(f"相关案例文档:\n{rag_context}")
        full_context = "\n\n".join(context_parts)

        if self.llm:
            generated_text = await asyncio.to_thread(
                self.llm.generate, filled_prompt, full_context
            )
        else:
            generated_text = f"[No LLM configured] {filled_prompt}\n\nContext: {full_context[:500]}"

        draft = TemplateDraft(
            template_id=template_id,
            template_label=template["label"],
            filled_fields=dict(field_values),
            generated_text=generated_text,
            research_results=research_results,
            created_at=datetime.now(UTC).isoformat(),
            case_id=case_id,
        )

        self._record_usage(template_id, field_values)
        return draft

    def _build_research_queries(self, template_id: str, field_values: dict[str, str]) -> list[str]:
        queries = []
        dispute = field_values.get("dispute_summary", "")
        matter = field_values.get("matter_type", "")
        if dispute:
            queries.append(f"{matter} {dispute} 法律规定 判例")
        if field_values.get("legal_basis"):
            queries.append(f"{field_values['legal_basis']} 法律条文 适用")
        if field_values.get("desired_outcome"):
            queries.append(f"法律途径 {field_values['desired_outcome']}")
        if field_values.get("case_description"):
            queries.append(f"{matter} {field_values['case_description']} 诉讼策略")
        return queries[:5]

    def _format_research_context(self, results: list[dict]) -> str:
        if not results:
            return ""
        lines = []
        for r in results[:6]:
            if r.get("title") and r.get("url"):
                lines.append(f"- {r['title']}: {r['url']}")
        return "\n".join(lines) if lines else ""

    def _record_usage(self, template_id: str, field_values: dict[str, str]):
        if template_id not in self.preferences.recent_templates:
            self.preferences.recent_templates.append(template_id)
        for field_name, field_value in field_values.items():
            if field_value and len(field_value) > 3:
                if template_id not in self.preferences.field_defaults:
                    self.preferences.field_defaults[template_id] = {}
                self.preferences.field_defaults[template_id][field_name] = field_value[:100]
        self.preferences.save()

    def save_draft(self, draft: TemplateDraft) -> str:
        draft_id = hashlib.sha256(
            f"{draft.template_id}:{draft.created_at}".encode()
        ).hexdigest()[:12]
        draft_dict = {
            "draft_id": draft_id,
            "template_id": draft.template_id,
            "template_label": draft.template_label,
            "filled_fields": draft.filled_fields,
            "generated_text": draft.generated_text,
            "edited_text": draft.edited_text,
            "research_results": draft.research_results,
            "created_at": draft.created_at,
            "case_id": draft.case_id,
            "final_adopted": draft.final_adopted,
        }
        self.preferences.drafts = [d for d in self.preferences.drafts if d.get("draft_id") != draft_id]
        self.preferences.drafts.append(draft_dict)
        self.preferences.save()
        return draft_id

    def update_draft(self, draft_id: str, edited_text: str, final_adopted: bool = False) -> bool:
        for draft in self.preferences.drafts:
            if draft.get("draft_id") == draft_id:
                draft["edited_text"] = edited_text
                draft["final_adopted"] = final_adopted
                self.preferences.save()
                return True
        return False

    def list_drafts(self, template_id: str | None = None, limit: int = 20) -> list[dict]:
        drafts = self.preferences.drafts[-limit:]
        if template_id:
            drafts = [d for d in drafts if d.get("template_id") == template_id]
        return list(reversed(drafts))

    def get_draft(self, draft_id: str) -> dict | None:
        for draft in self.preferences.drafts:
            if draft.get("draft_id") == draft_id:
                return draft
        return None

    def set_preference(self, key: str, value: Any):
        if hasattr(self.preferences, key):
            setattr(self.preferences, key, value)
            self.preferences.save()

    def get_preferences(self) -> dict:
        return {
            "default_template_id": self.preferences.default_template_id,
            "default_matter_type": self.preferences.default_matter_type,
            "auto_research_enabled": self.preferences.auto_research_enabled,
            "research_depth": self.preferences.research_depth,
            "recent_templates": self.preferences.recent_templates[-10:],
            "total_saved_drafts": len(self.preferences.drafts),
        }
