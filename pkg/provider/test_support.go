package provider

import (
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// NewTestProvider builds an ApiProvider backed by the supplied SlackAPI client,
// with the users and channels caches pre-marked as ready. It exists so that
// handler unit tests in other packages can inject a mock Slack client without
// real credentials or network access. It is intentionally minimal and must not
// be used outside of tests.
func NewTestProvider(client SlackAPI, logger *zap.Logger) *ApiProvider {
	if logger == nil {
		logger = zap.NewNop()
	}

	ap := &ApiProvider{
		client: client,
		logger: logger,
	}
	ap.usersSnapshot.Store(&UsersCache{
		Users:    make(map[string]slack.User),
		UsersInv: make(map[string]string),
	})
	ap.channelsSnapshot.Store(&ChannelsCache{
		Channels:    make(map[string]Channel),
		ChannelsInv: make(map[string]string),
	})
	ap.SkipCache() // mark users/channels caches ready so IsReady() passes
	return ap
}
