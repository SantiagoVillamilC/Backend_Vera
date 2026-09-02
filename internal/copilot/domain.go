package copilot

type Intent string
const ( IntentQuery Intent = "query"; IntentTransaction Intent = "transaction" )
type ChatMessage struct { Role, Content string }
type Request struct { Messages []ChatMessage }
type Response struct { Intent Intent; Message string }
