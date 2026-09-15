package copilot

// Intent clasifica la intención detectada en un mensaje del usuario.
type Intent string

// Intenciones que el copilot puede reconocer.
const (
	IntentQuery       Intent = "query"
	IntentTransaction Intent = "transaction"
)

// ChatMessage es un turno de la conversación.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request es el cuerpo de POST /copilot.
type Request struct {
	Messages []ChatMessage `json:"messages" validate:"required,min=1"`
}

// Response es la respuesta del copilot.
type Response struct {
	Intent  Intent `json:"intent"`
	Message string `json:"message"`
}
