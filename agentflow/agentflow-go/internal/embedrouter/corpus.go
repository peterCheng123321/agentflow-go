package embedrouter

// LabeledUtterance is one example sentence + its intent label. The
// canonical corpus below is checked into source so the router's behaviour
// is reproducible — no separate JSON file to drift out of sync.
type LabeledUtterance struct {
	Label     string
	Utterance string
}

// Intent labels — kept identical to the LLM-router (server.IntentTools etc.)
// so callers can swap implementations without touching downstream code.
const (
	IntentTools          = "NEEDS_TOOLS"
	IntentRAG            = "NEEDS_RAG"
	IntentConversational = "CONVERSATIONAL"
)

// DefaultCorpus is the seed reference set for AgentFlow's 3-class router.
// Each label has 7-9 utterances covering English + Chinese + the
// disambiguating edges we know the LLM router struggled with
// (capability questions, "what does X say" RAG framing, scheduling/email
// actions). Add new utterances here when production logs surface a
// repeatedly mis-routed pattern.
var DefaultCorpus = []LabeledUtterance{
	// NEEDS_TOOLS — structured action / lookup.
	{IntentTools, "What's the deadline for case 8421?"},
	{IntentTools, "When is the next hearing for case 12345?"},
	{IntentTools, "Schedule a client meeting tomorrow at 3pm"},
	{IntentTools, "Email the contract to alice@firm.com"},
	{IntentTools, "Mark case 5567 as closed"},
	{IntentTools, "Show me my pending cases"},
	{IntentTools, "List all open matters"},
	{IntentTools, "案件123的状态是什么？"},
	{IntentTools, "查找张三的案件信息"},
	{IntentTools, "把案件标记为已结案"},

	// NEEDS_RAG — synthesize from the user's documents.
	{IntentRAG, "Summarize the indemnification clauses in this contract"},
	{IntentRAG, "What does the loan agreement say about prepayment penalties?"},
	{IntentRAG, "Find all references to liability in the case files"},
	{IntentRAG, "Compare the warranty terms in these two agreements"},
	{IntentRAG, "What evidence supports the plaintiff's claim?"},
	{IntentRAG, "Explain how 'force majeure' is defined here"},
	{IntentRAG, "总结这份合同的保密条款"},
	{IntentRAG, "在文件中找出所有提到违约金的段落"},
	{IntentRAG, "这份判决书的主要观点是什么"},

	// CONVERSATIONAL — greetings, smalltalk, capability questions.
	{IntentConversational, "Hi, how are you?"},
	{IntentConversational, "Thanks for your help!"},
	{IntentConversational, "What can you help me with?"},
	{IntentConversational, "Good morning"},
	{IntentConversational, "Tell me about yourself"},
	{IntentConversational, "How does this app work?"},
	{IntentConversational, "你好"},
	{IntentConversational, "你能做什么？"},
	{IntentConversational, "谢谢"},
}
