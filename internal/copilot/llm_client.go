package copilot

import ("context"; "fmt"; "os")
type LLMClient struct { endpoint, apiKey string }
func NewLLMClient() (*LLMClient,error) { key:=os.Getenv("GROQ_API_KEY"); endpoint:="https://api.groq.com/openai/v1/chat/completions";if key==""{key=os.Getenv("OPENAI_API_KEY");endpoint="https://api.openai.com/v1/chat/completions"};if key==""{return nil,fmt.Errorf("GROQ_API_KEY or OPENAI_API_KEY is required")};return &LLMClient{endpoint:endpoint,apiKey:key},nil }
func (c *LLMClient) Complete(ctx context.Context,messages []ChatMessage)(string,error){return "",fmt.Errorf("TODO: call configured LLM API")}
