package platform

import (
	"strconv"
	"strings"
)

const cliProxyOpenAICompatiblePrefix = "openai-compatible-"

const (
	ProviderUnknown     = "unknown"
	ProviderOpenAI      = "openai"
	ProviderAzure       = "azure"
	ProviderOllama      = "ollama"
	ProviderOpenRouter  = "openrouter"
	ProviderAnthropic   = "anthropic"
	ProviderGemini      = "gemini"
	ProviderPaLM        = "palm"
	ProviderVertexAI    = "vertexai"
	ProviderAWS         = "aws"
	ProviderQwen        = "qwen"
	ProviderBaidu       = "baidu"
	ProviderZhipu       = "zhipu"
	ProviderXunfei      = "xunfei"
	ProviderTencent     = "tencent"
	ProviderCohere      = "cohere"
	ProviderMistral     = "mistral"
	ProviderDeepSeek    = "deepseek"
	ProviderMoonshot    = "moonshot"
	ProviderMiniMax     = "minimax"
	ProviderSiliconFlow = "siliconflow"
	ProviderVolcEngine  = "volcengine"
	ProviderXAI         = "xai"
	ProviderPerplexity  = "perplexity"
	ProviderCloudflare  = "cloudflare"
	ProviderDify        = "dify"
	ProviderJina        = "jina"
	ProviderCoze        = "coze"
	ProviderReplicate   = "replicate"
	ProviderCodex       = "codex"
	ProviderCustom      = "custom"
	ProviderAntigravity = "antigravity"
	ProviderAIStudio    = "aistudio"
	ProviderVertex      = "vertex"
	ProviderKimi        = "kimi"
	ProviderKiro        = "kiro"

	ProviderMidjourney     = "midjourney"
	ProviderMidjourneyPlus = "midjourney_plus"
	ProviderOpenAIMax      = "openaimax"
	ProviderOhMyGPT        = "ohmygpt"
	ProviderAILS           = "ails"
	ProviderAIProxy        = "aiproxy"
	ProviderAPI2GPT        = "api2gpt"
	ProviderAIGC2D         = "aigc2d"
	Provider360            = "360"
	ProviderAIProxyLibrary = "aiproxy_library"
	ProviderFastGPT        = "fastgpt"
	ProviderLingYiWanWu    = "lingyiwanwu"
	ProviderSuno           = "suno"
	ProviderMokaAI         = "mokaai"
	ProviderXinference     = "xinference"
	ProviderKling          = "kling"
	ProviderJimeng         = "jimeng"
	ProviderVidu           = "vidu"
	ProviderSubmodel       = "submodel"
	ProviderDoubaoVideo    = "doubao_video"
	ProviderSora           = "sora"
)

type ProviderDescriptor struct {
	ID            string
	Name          string
	RawType       string
	DiscoveryOnly bool
}

type ProviderCatalog struct {
	newAPI map[int]ProviderDescriptor
	cpa    map[string]ProviderDescriptor
}

func DefaultCatalog() *ProviderCatalog {
	newAPI := map[int]ProviderDescriptor{
		1:  {ID: ProviderOpenAI, Name: "OpenAI"},
		2:  {ID: ProviderMidjourney, Name: "Midjourney", DiscoveryOnly: true},
		3:  {ID: ProviderAzure, Name: "Azure"},
		4:  {ID: ProviderOllama, Name: "Ollama"},
		5:  {ID: ProviderMidjourneyPlus, Name: "MidjourneyPlus", DiscoveryOnly: true},
		6:  {ID: ProviderOpenAIMax, Name: "OpenAIMax", DiscoveryOnly: true},
		7:  {ID: ProviderOhMyGPT, Name: "OhMyGPT", DiscoveryOnly: true},
		8:  {ID: ProviderCustom, Name: "Custom", DiscoveryOnly: true},
		9:  {ID: ProviderAILS, Name: "AILS", DiscoveryOnly: true},
		10: {ID: ProviderAIProxy, Name: "AIProxy", DiscoveryOnly: true},
		11: {ID: ProviderPaLM, Name: "PaLM"},
		12: {ID: ProviderAPI2GPT, Name: "API2GPT", DiscoveryOnly: true},
		13: {ID: ProviderAIGC2D, Name: "AIGC2D", DiscoveryOnly: true},
		14: {ID: ProviderAnthropic, Name: "Anthropic"},
		15: {ID: ProviderBaidu, Name: "Baidu"},
		16: {ID: ProviderZhipu, Name: "Zhipu"},
		17: {ID: ProviderQwen, Name: "Ali"},
		18: {ID: ProviderXunfei, Name: "Xunfei"},
		19: {ID: Provider360, Name: "360", DiscoveryOnly: true},
		20: {ID: ProviderOpenRouter, Name: "OpenRouter"},
		21: {ID: ProviderAIProxyLibrary, Name: "AIProxyLibrary", DiscoveryOnly: true},
		22: {ID: ProviderFastGPT, Name: "FastGPT", DiscoveryOnly: true},
		23: {ID: ProviderTencent, Name: "Tencent"},
		24: {ID: ProviderGemini, Name: "Gemini"},
		25: {ID: ProviderMoonshot, Name: "Moonshot"},
		26: {ID: ProviderZhipu, Name: "ZhipuV4"},
		27: {ID: ProviderPerplexity, Name: "Perplexity"},
		31: {ID: ProviderLingYiWanWu, Name: "LingYiWanWu", DiscoveryOnly: true},
		33: {ID: ProviderAWS, Name: "AWS"},
		34: {ID: ProviderCohere, Name: "Cohere"},
		35: {ID: ProviderMiniMax, Name: "MiniMax"},
		36: {ID: ProviderSuno, Name: "SunoAPI", DiscoveryOnly: true},
		37: {ID: ProviderDify, Name: "Dify"},
		38: {ID: ProviderJina, Name: "Jina"},
		39: {ID: ProviderCloudflare, Name: "Cloudflare"},
		40: {ID: ProviderSiliconFlow, Name: "SiliconFlow"},
		41: {ID: ProviderVertexAI, Name: "VertexAI"},
		42: {ID: ProviderMistral, Name: "Mistral"},
		43: {ID: ProviderDeepSeek, Name: "DeepSeek"},
		44: {ID: ProviderMokaAI, Name: "MokaAI", DiscoveryOnly: true},
		45: {ID: ProviderVolcEngine, Name: "VolcEngine"},
		46: {ID: ProviderBaidu, Name: "BaiduV2"},
		47: {ID: ProviderXinference, Name: "Xinference", DiscoveryOnly: true},
		48: {ID: ProviderXAI, Name: "xAI"},
		49: {ID: ProviderCoze, Name: "Coze"},
		50: {ID: ProviderKling, Name: "Kling", DiscoveryOnly: true},
		51: {ID: ProviderJimeng, Name: "Jimeng", DiscoveryOnly: true},
		52: {ID: ProviderVidu, Name: "Vidu", DiscoveryOnly: true},
		53: {ID: ProviderSubmodel, Name: "Submodel", DiscoveryOnly: true},
		54: {ID: ProviderDoubaoVideo, Name: "DoubaoVideo", DiscoveryOnly: true},
		55: {ID: ProviderSora, Name: "Sora", DiscoveryOnly: true},
		56: {ID: ProviderReplicate, Name: "Replicate"},
		57: {ID: ProviderCodex, Name: "ChatGPT Subscription (Codex)"},
		58: {ID: ProviderCustom, Name: "Advanced Custom", DiscoveryOnly: true},
	}
	for rawType, descriptor := range newAPI {
		descriptor.RawType = strconv.Itoa(rawType)
		newAPI[rawType] = descriptor
	}

	cpa := map[string]ProviderDescriptor{
		"antigravity":          {ID: ProviderAntigravity, Name: "Antigravity"},
		"claude":               {ID: ProviderAnthropic, Name: "Claude"},
		"anthropic":            {ID: ProviderAnthropic, Name: "Claude"},
		"codex":                {ID: ProviderCodex, Name: "Codex"},
		"gemini":               {ID: ProviderGemini, Name: "Gemini"},
		"gemini-cli":           {ID: ProviderGemini, Name: "Gemini CLI"},
		"gemini-interactions":  {ID: ProviderGemini, Name: "Google Interactions"},
		"aistudio":             {ID: ProviderAIStudio, Name: "AI Studio"},
		"vertex":               {ID: ProviderVertex, Name: "Vertex"},
		"kimi":                 {ID: ProviderKimi, Name: "Kimi"},
		"kiro":                 {ID: ProviderKiro, Name: "Kiro"},
		"xai":                  {ID: ProviderXAI, Name: "xAI"},
		"openai":               {ID: ProviderOpenAI, Name: "OpenAI"},
		"openai-compatibility": {ID: ProviderOpenAI, Name: "OpenAI Compatibility"},
	}
	for rawType, descriptor := range cpa {
		descriptor.RawType = rawType
		cpa[rawType] = descriptor
	}
	return &ProviderCatalog{newAPI: newAPI, cpa: cpa}
}

func (c *ProviderCatalog) FromNewAPI(rawType int) ProviderDescriptor {
	if c != nil {
		if descriptor, ok := c.newAPI[rawType]; ok {
			return descriptor
		}
	}
	return ProviderDescriptor{ID: ProviderUnknown, Name: "Unknown", RawType: strconv.Itoa(rawType), DiscoveryOnly: true}
}

func (c *ProviderCatalog) FromCLIProxyAPI(rawType string) ProviderDescriptor {
	normalized := strings.ToLower(strings.TrimSpace(rawType))
	if c != nil {
		if descriptor, ok := c.cpa[normalized]; ok {
			return descriptor
		}
	}
	if IsCLIProxyOpenAICompatibleProvider(normalized) {
		return ProviderDescriptor{ID: ProviderOpenAI, Name: "OpenAI Compatibility", RawType: normalized}
	}
	return ProviderDescriptor{ID: ProviderUnknown, Name: strings.TrimSpace(rawType), RawType: normalized, DiscoveryOnly: true}
}

func IsCLIProxyOpenAICompatibleProvider(rawType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(rawType))
	name, ok := strings.CutPrefix(normalized, cliProxyOpenAICompatiblePrefix)
	if !ok || name == "" || !isASCIIAlphaNumeric(name[0]) || !isASCIIAlphaNumeric(name[len(name)-1]) {
		return false
	}
	for i := 1; i < len(name)-1; i++ {
		character := name[i]
		if !isASCIIAlphaNumeric(character) && character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func isASCIIAlphaNumeric(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
}

func ChannelAssetID(sourceID, channelID string, keyIndex *int) string {
	id := strings.TrimSpace(sourceID) + ":channel:" + strings.TrimSpace(channelID)
	if keyIndex != nil {
		id += ":key:" + strconv.Itoa(*keyIndex)
	}
	return id
}

func TokenAssetID(sourceID string, tokenID int) string {
	return strings.TrimSpace(sourceID) + ":token:" + strconv.Itoa(tokenID)
}

func CLIProxyAssetID(sourceID, authID string) string {
	return strings.TrimSpace(sourceID) + ":cpa:" + strings.TrimSpace(authID)
}
