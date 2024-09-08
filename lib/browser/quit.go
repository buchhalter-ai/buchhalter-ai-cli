package browser

import (
	"context"

	"github.com/chromedp/chromedp"
)

// Quit cancels a chromedp context, waits for its resources to be cleaned up,
// and returns any error encountered during that process.
//
// ctx needs to be a chromedp.Context to work properly.
func Quit(ctx context.Context) error {
	return chromedp.Cancel(ctx)
}
