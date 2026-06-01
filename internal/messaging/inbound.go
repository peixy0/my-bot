package messaging

import "context"

type Inbound interface {
	Run(ctx context.Context) error
}
