package worker

import (
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
)

// TokenLastUseName names the worker writing API tokens' last use in logs,
// metrics and Tick.
const TokenLastUseName = "token_last_use"

// TokenLastUse returns the worker writing the uses of API tokens the replica
// noted, once a minute: a token's last use is recorded at most that often
// anyway, and a request never waits on the write.
func TokenLastUse(uses *appaccess.TokenUses) Worker {
	return Worker{Name: TokenLastUseName, Interval: time.Minute, Round: uses.Flush}
}
