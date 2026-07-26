package platform

import (
	"errors"
	"strconv"
	"testing"
)

func TestDefaultCatalogNormalizesKnownAndUnknownProviders(t *testing.T) {
	t.Parallel()

	catalog := DefaultCatalog()

	openAI := catalog.FromNewAPI(1)
	if openAI.ID != ProviderOpenAI || openAI.Name != "OpenAI" || openAI.DiscoveryOnly {
		t.Fatalf("New API type 1 = %#v", openAI)
	}

	claude := catalog.FromCLIProxyAPI(" Claude ")
	if claude.ID != ProviderAnthropic || claude.Name != "Claude" || claude.DiscoveryOnly {
		t.Fatalf("CLIProxyAPI claude = %#v", claude)
	}

	unknownNewAPI := catalog.FromNewAPI(999)
	if unknownNewAPI.ID != ProviderUnknown || unknownNewAPI.RawType != "999" || !unknownNewAPI.DiscoveryOnly {
		t.Fatalf("unknown New API provider = %#v", unknownNewAPI)
	}

	unknownCPA := catalog.FromCLIProxyAPI("future-plugin")
	if unknownCPA.ID != ProviderUnknown || unknownCPA.RawType != "future-plugin" || !unknownCPA.DiscoveryOnly {
		t.Fatalf("unknown CLIProxyAPI provider = %#v", unknownCPA)
	}
}

func TestDefaultCatalogNormalizesRealCLIProxyAPIStaticProviders(t *testing.T) {
	t.Parallel()

	catalog := DefaultCatalog()
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "gemini-cli", want: ProviderGemini},
		{raw: "gemini-interactions", want: ProviderGemini},
		{raw: "openai-compatible-custom-provider", want: ProviderOpenAI},
		{raw: " OPENAI-COMPATIBLE-TEAM_1 ", want: ProviderOpenAI},
	}
	for _, test := range tests {
		descriptor := catalog.FromCLIProxyAPI(test.raw)
		if descriptor.ID != test.want || descriptor.DiscoveryOnly {
			t.Fatalf("FromCLIProxyAPI(%q) = %#v", test.raw, descriptor)
		}
	}

	for _, raw := range []string{
		"openai-compatible-",
		"openai-compatible-../../future",
		"openai-compatible-team/name",
		"future-openai-compatible-team",
	} {
		descriptor := catalog.FromCLIProxyAPI(raw)
		if descriptor.ID != ProviderUnknown || !descriptor.DiscoveryOnly {
			t.Fatalf("unsafe provider %q = %#v", raw, descriptor)
		}
	}
}

func TestStableAssetIDs(t *testing.T) {
	t.Parallel()

	if got := ChannelAssetID("source-a", "42", nil); got != "source-a:channel:42" {
		t.Fatalf("single-key asset id = %q", got)
	}
	index := 3
	if got := ChannelAssetID("source-a", "42", &index); got != "source-a:channel:42:key:3" {
		t.Fatalf("multi-key asset id = %q", got)
	}
	if got := CLIProxyAssetID("source-a", "auth-id-7"); got != "source-a:cpa:auth-id-7" {
		t.Fatalf("CLIProxyAPI asset id = %q", got)
	}
}

func TestDefaultCatalogPreservesEveryKnownNewAPIChannelType(t *testing.T) {
	t.Parallel()

	type expectedProvider struct {
		id            string
		name          string
		discoveryOnly bool
	}
	expected := map[int]expectedProvider{
		0:  {id: "unknown", name: "Unknown", discoveryOnly: true},
		1:  {id: "openai", name: "OpenAI"},
		2:  {id: "midjourney", name: "Midjourney", discoveryOnly: true},
		3:  {id: "azure", name: "Azure"},
		4:  {id: "ollama", name: "Ollama"},
		5:  {id: "midjourney_plus", name: "MidjourneyPlus", discoveryOnly: true},
		6:  {id: "openaimax", name: "OpenAIMax", discoveryOnly: true},
		7:  {id: "ohmygpt", name: "OhMyGPT", discoveryOnly: true},
		8:  {id: "custom", name: "Custom", discoveryOnly: true},
		9:  {id: "ails", name: "AILS", discoveryOnly: true},
		10: {id: "aiproxy", name: "AIProxy", discoveryOnly: true},
		11: {id: "palm", name: "PaLM"},
		12: {id: "api2gpt", name: "API2GPT", discoveryOnly: true},
		13: {id: "aigc2d", name: "AIGC2D", discoveryOnly: true},
		14: {id: "anthropic", name: "Anthropic"},
		15: {id: "baidu", name: "Baidu"},
		16: {id: "zhipu", name: "Zhipu"},
		17: {id: "qwen", name: "Ali"},
		18: {id: "xunfei", name: "Xunfei"},
		19: {id: "360", name: "360", discoveryOnly: true},
		20: {id: "openrouter", name: "OpenRouter"},
		21: {id: "aiproxy_library", name: "AIProxyLibrary", discoveryOnly: true},
		22: {id: "fastgpt", name: "FastGPT", discoveryOnly: true},
		23: {id: "tencent", name: "Tencent"},
		24: {id: "gemini", name: "Gemini"},
		25: {id: "moonshot", name: "Moonshot"},
		26: {id: "zhipu", name: "ZhipuV4"},
		27: {id: "perplexity", name: "Perplexity"},
		31: {id: "lingyiwanwu", name: "LingYiWanWu", discoveryOnly: true},
		33: {id: "aws", name: "AWS"},
		34: {id: "cohere", name: "Cohere"},
		35: {id: "minimax", name: "MiniMax"},
		36: {id: "suno", name: "SunoAPI", discoveryOnly: true},
		37: {id: "dify", name: "Dify"},
		38: {id: "jina", name: "Jina"},
		39: {id: "cloudflare", name: "Cloudflare"},
		40: {id: "siliconflow", name: "SiliconFlow"},
		41: {id: "vertexai", name: "VertexAI"},
		42: {id: "mistral", name: "Mistral"},
		43: {id: "deepseek", name: "DeepSeek"},
		44: {id: "mokaai", name: "MokaAI", discoveryOnly: true},
		45: {id: "volcengine", name: "VolcEngine"},
		46: {id: "baidu", name: "BaiduV2"},
		47: {id: "xinference", name: "Xinference", discoveryOnly: true},
		48: {id: "xai", name: "xAI"},
		49: {id: "coze", name: "Coze"},
		50: {id: "kling", name: "Kling", discoveryOnly: true},
		51: {id: "jimeng", name: "Jimeng", discoveryOnly: true},
		52: {id: "vidu", name: "Vidu", discoveryOnly: true},
		53: {id: "submodel", name: "Submodel", discoveryOnly: true},
		54: {id: "doubao_video", name: "DoubaoVideo", discoveryOnly: true},
		55: {id: "sora", name: "Sora", discoveryOnly: true},
		56: {id: "replicate", name: "Replicate"},
		57: {id: "codex", name: "ChatGPT Subscription (Codex)"},
		58: {id: "custom", name: "Advanced Custom", discoveryOnly: true},
	}

	catalog := DefaultCatalog()
	for rawType, want := range expected {
		descriptor := catalog.FromNewAPI(rawType)
		if descriptor.ID != want.id || descriptor.Name != want.name || descriptor.RawType != strconv.Itoa(rawType) || descriptor.DiscoveryOnly != want.discoveryOnly {
			t.Errorf("FromNewAPI(%d) = %#v, want %#v", rawType, descriptor, want)
		}
	}
}

func TestSelectSyncModeUsesExplicitCompatibility(t *testing.T) {
	t.Parallel()

	staticAsset := UpstreamAsset{
		SourceType: "cliproxyapi",
		Provider:   ProviderOpenAI,
		Kind:       AssetStaticAPIKey,
	}
	caps := TargetCapabilities{
		Platform: "newapi",
		Providers: map[string]ProviderCapability{
			ProviderOpenAI: {Modes: []SyncMode{SyncModeStaticKey}},
		},
	}
	mode, err := SelectSyncMode(staticAsset, caps)
	if err != nil || mode != SyncModeStaticKey {
		t.Fatalf("SelectSyncMode(static) = %q, %v", mode, err)
	}

	nativeAsset := UpstreamAsset{
		SourceType: "cliproxyapi",
		Provider:   ProviderAnthropic,
		Kind:       AssetOAuthFile,
		Metadata:   map[string]string{"schema_version": "cpa-auth-v1"},
	}
	nativeCaps := TargetCapabilities{
		Platform:         "cliproxyapi",
		NativeAuthSchema: "cpa-auth-v1",
		Providers: map[string]ProviderCapability{
			ProviderAnthropic: {Modes: []SyncMode{SyncModeNativeAuthFile}},
		},
	}
	mode, err = SelectSyncMode(nativeAsset, nativeCaps)
	if err != nil || mode != SyncModeNativeAuthFile {
		t.Fatalf("SelectSyncMode(native) = %q, %v", mode, err)
	}

	proxyAsset := nativeAsset
	proxyAsset.Kind = AssetProxyKey
	proxyAsset.BaseURL = "https://proxy.example.com/v1"
	proxyCaps := TargetCapabilities{
		Platform: "newapi",
		Providers: map[string]ProviderCapability{
			ProviderAnthropic: {Modes: []SyncMode{SyncModeProxyEndpoint}},
		},
	}
	mode, err = SelectSyncMode(proxyAsset, proxyCaps)
	if err != nil || mode != SyncModeProxyEndpoint {
		t.Fatalf("SelectSyncMode(proxy) = %q, %v", mode, err)
	}

	oauthFallbackCases := []struct {
		name  string
		asset UpstreamAsset
		caps  TargetCapabilities
	}{
		{name: "cross platform", asset: UpstreamAsset{
			SourceType: "cliproxyapi", Provider: ProviderAnthropic, Kind: AssetOAuthFile,
			BaseURL: "https://proxy.example.com/v1", Metadata: map[string]string{"schema_version": "cpa-auth-v1"},
		}, caps: proxyCaps},
		{name: "schema mismatch with proxy available", asset: UpstreamAsset{
			SourceType: "cliproxyapi", Provider: ProviderAnthropic, Kind: AssetOAuthFile,
			BaseURL: "https://proxy.example.com/v1", Metadata: map[string]string{"schema_version": "future-schema"},
		}, caps: TargetCapabilities{
			Platform: "cliproxyapi", NativeAuthSchema: "cpa-auth-v1",
			Providers: map[string]ProviderCapability{ProviderAnthropic: {Modes: []SyncMode{SyncModeNativeAuthFile, SyncModeProxyEndpoint}}},
		}},
		{name: "missing source schema", asset: UpstreamAsset{
			SourceType: "cliproxyapi", Provider: ProviderAnthropic, Kind: AssetOAuthFile,
			BaseURL: "https://proxy.example.com/v1",
		}, caps: nativeCaps},
		{name: "missing target schema", asset: nativeAsset, caps: TargetCapabilities{
			Platform:  "cliproxyapi",
			Providers: map[string]ProviderCapability{ProviderAnthropic: {Modes: []SyncMode{SyncModeNativeAuthFile, SyncModeProxyEndpoint}}},
		}},
		{name: "missing platform identities", asset: UpstreamAsset{
			Provider: ProviderAnthropic, Kind: AssetOAuthFile,
			Metadata: map[string]string{"schema_version": "cpa-auth-v1"},
		}, caps: TargetCapabilities{
			NativeAuthSchema: "cpa-auth-v1",
			Providers:        map[string]ProviderCapability{ProviderAnthropic: {Modes: []SyncMode{SyncModeNativeAuthFile}}},
		}},
		{name: "blank schema identities", asset: UpstreamAsset{
			SourceType: "cliproxyapi", Provider: ProviderAnthropic, Kind: AssetOAuthFile,
			Metadata: map[string]string{"schema_version": " "},
		}, caps: TargetCapabilities{
			Platform: "cliproxyapi", NativeAuthSchema: " ",
			Providers: map[string]ProviderCapability{ProviderAnthropic: {Modes: []SyncMode{SyncModeNativeAuthFile}}},
		}},
	}
	for _, test := range oauthFallbackCases {
		t.Run(test.name, func(t *testing.T) {
			if got, selectErr := SelectSyncMode(test.asset, test.caps); !errors.Is(selectErr, ErrIncompatibleTarget) {
				t.Fatalf("SelectSyncMode() = %q, %v; want ErrIncompatibleTarget", got, selectErr)
			}
		})
	}

	unknown := staticAsset
	unknown.Provider = ProviderUnknown
	if _, err = SelectSyncMode(unknown, caps); !errors.Is(err, ErrIncompatibleTarget) {
		t.Fatalf("SelectSyncMode(unknown) error = %v", err)
	}

	schemaMismatch := nativeAsset
	schemaMismatch.Metadata = map[string]string{"schema_version": "future-schema"}
	if _, err = SelectSyncMode(schemaMismatch, nativeCaps); !errors.Is(err, ErrIncompatibleTarget) {
		t.Fatalf("SelectSyncMode(schema mismatch) error = %v", err)
	}
}
