// input: 固定样本工单 ID、读取错误及 Zoho 只读历史接口。
// output: 以明确生命周期事件确认合并/删除；无法确认时保留原读取错误。
// pos: Zoho 失效工单复核边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package zoho

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"zoho-workstation/internal/ticket"
)

var ErrMerged = errors.New("ticket confirmed merged in Zoho")

// Fetch checks recent lifecycle evidence only for deleted or unavailable tickets.
// A missing listing, HTTP 404, or inaccessible history alone never retires an ID.
func (c *Client) Fetch(ctx context.Context, id string) (ticket.RawSnapshot, error) {
	raw, err := c.fetchSnapshot(ctx, id)
	var response *responseError
	missing := errors.As(err, &response) && response.method == http.MethodGet &&
		(response.status == http.StatusNotFound || response.status == http.StatusGone)
	if !errors.Is(err, ErrDeleted) && !missing {
		return raw, err
	}
	if ctx.Err() != nil {
		return raw, ctx.Err()
	}
	// History has its own documented pagination and no message IDs. Only the
	// latest 50 events are probed; no evidence means retain the retry obligation.
	body, historyErr := c.get(ctx, "/tickets/"+id+"/History", url.Values{"from": {"0"}, "limit": {"50"}})
	if historyErr != nil {
		if RetryAfter(historyErr) > 0 || ctx.Err() != nil {
			return raw, historyErr
		}
		return raw, err
	}
	rows, parseErr := list(body)
	if parseErr != nil || len(rows) > 50 {
		return raw, err
	}
	for _, row := range rows {
		switch field(row, "eventName") {
		case "TicketMergedChild":
			return ticket.RawSnapshot{}, ErrMerged
		case "TicketDeleted":
			return ticket.RawSnapshot{}, ErrDeleted
		case "TicketRestored", "TicketCreated":
			return raw, err
		}
	}
	return raw, err
}
