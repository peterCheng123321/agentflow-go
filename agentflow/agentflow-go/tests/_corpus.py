"""
Mirror of the canonical reference corpora that live in the Go source.
Kept in sync by hand (small lists; auto-sync is overkill for now).

Source of truth:
  - INTENT_CORPUS: agentflow-go/internal/embedrouter/corpus.go
                   (DefaultCorpus)
  - MATTER_CORPUS: agentflow-go/internal/llmutil/matter_router.go
                   (MatterCorpus)

If you change the Go corpus, update these mirrors and re-run the suite.
"""

# (label, utterance)
INTENT_CORPUS = [
    # NEEDS_TOOLS
    ("NEEDS_TOOLS", "What's the deadline for case 8421?"),
    ("NEEDS_TOOLS", "When is the next hearing for case 12345?"),
    ("NEEDS_TOOLS", "Schedule a client meeting tomorrow at 3pm"),
    ("NEEDS_TOOLS", "Email the contract to alice@firm.com"),
    ("NEEDS_TOOLS", "Mark case 5567 as closed"),
    ("NEEDS_TOOLS", "Show me my pending cases"),
    ("NEEDS_TOOLS", "List all open matters"),
    ("NEEDS_TOOLS", "案件123的状态是什么？"),
    ("NEEDS_TOOLS", "查找张三的案件信息"),
    ("NEEDS_TOOLS", "把案件标记为已结案"),

    # NEEDS_RAG
    ("NEEDS_RAG", "Summarize the indemnification clauses in this contract"),
    ("NEEDS_RAG", "What does the loan agreement say about prepayment penalties?"),
    ("NEEDS_RAG", "Find all references to liability in the case files"),
    ("NEEDS_RAG", "Compare the warranty terms in these two agreements"),
    ("NEEDS_RAG", "What evidence supports the plaintiff's claim?"),
    ("NEEDS_RAG", "Explain how 'force majeure' is defined here"),
    ("NEEDS_RAG", "总结这份合同的保密条款"),
    ("NEEDS_RAG", "在文件中找出所有提到违约金的段落"),
    ("NEEDS_RAG", "这份判决书的主要观点是什么"),

    # CONVERSATIONAL
    ("CONVERSATIONAL", "Hi, how are you?"),
    ("CONVERSATIONAL", "Thanks for your help!"),
    ("CONVERSATIONAL", "What can you help me with?"),
    ("CONVERSATIONAL", "Good morning"),
    ("CONVERSATIONAL", "Tell me about yourself"),
    ("CONVERSATIONAL", "How does this app work?"),
    ("CONVERSATIONAL", "你好"),
    ("CONVERSATIONAL", "你能做什么？"),
    ("CONVERSATIONAL", "谢谢"),
]

MATTER_CORPUS = [
    ("Civil Litigation",         "民事起诉状"),
    ("Civil Litigation",         "起诉状"),
    ("Civil Litigation",         "民事诉讼"),
    ("Civil Litigation",         "起诉书"),

    ("Contract Dispute",         "合同纠纷"),
    ("Contract Dispute",         "合同履行争议"),
    ("Contract Dispute",         "合同违约"),

    ("Sales Contract Dispute",   "买卖合同纠纷"),
    ("Sales Contract Dispute",   "货物销售合同"),
    ("Sales Contract Dispute",   "购销合同争议"),

    ("Debt Dispute",             "欠款追讨"),
    ("Debt Dispute",             "债务清偿纠纷"),
    ("Debt Dispute",             "欠款纠纷"),

    ("Loan Dispute",             "借款合同纠纷"),
    ("Loan Dispute",             "民间借贷"),
    ("Loan Dispute",             "借款争议"),

    ("Lease Dispute",            "房屋租赁合同纠纷"),
    ("Lease Dispute",            "租金欠付"),
    ("Lease Dispute",            "住宅租赁争议"),

    ("Commercial Lease Dispute", "商铺租赁合同"),
    ("Commercial Lease Dispute", "商业租赁纠纷"),
    ("Commercial Lease Dispute", "写字楼租赁争议"),

    ("Labor Dispute",            "劳动合同纠纷"),
    ("Labor Dispute",            "工资欠付"),
    ("Labor Dispute",            "劳务争议"),
    ("Labor Dispute",            "解除劳动关系"),
]

# Margin thresholds — must match the Go config defaults so test predictions
# track production behaviour exactly.
INTENT_MARGIN = 0.05
MATTER_MARGIN = 0.01
