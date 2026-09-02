package transactions

import "context"
type RealtimePublisher interface { Publish(context.Context, Transaction) error }
