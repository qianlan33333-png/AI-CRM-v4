package port

import "context"

type AIModelSelection struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Configured bool   `json:"configured"`
	Version    int64  `json:"version"`
}
type AIModelCommand struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	APIKey          string `json:"api_key"`
	ExpectedVersion int64  `json:"expected_version"`
	ActorID         int64  `json:"-"`
}
type AIModelRuntime struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	APIKey  string `json:"-"`
}
type AIModelSettings interface {
	ReadAIModel(context.Context) (AIModelSelection, error)
	SaveAIModel(context.Context, AIModelCommand) (AIModelSelection, error)
	ReadAIModelRuntime(context.Context) (AIModelRuntime, bool, error)
}
