package agent

// cold-digest §6: graph_overview folding + the restore tools.
//
//	coldDigestsRecent — builds the folded cold region for graph_overview:
//	  cold_digests (flat {id, body, member_count}), newest-member first, capped.
//	expand_digest(id)  — level-1 restore: a digest's member compact list.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// digestMemberEntry builds the compact per-member view expand_digest returns —
// same shape as recent_facts / recent_done_intents (§6.1 middle level). store is
// the digest's OWNING store (the current task, or a read-only source task §2).
func (t *ToolSet) digestMemberEntry(store *db.ExplorationStore, id int64) map[string]any {
	n, _ := store.GetNode(id)
	if n == nil {
		return map[string]any{"id": id, "missing": true}
	}
	m := compactNode(n)
	m["state"] = n.State
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if c, ok := p["confidence"].(string); ok && c != "" {
			m["confidence"] = c
		}
	}
	return m
}

// coldDigestsRecent returns a store's active digests as flat bodies for
// graph_overview, ordered by the recency of their freshest member (max member id ≈
// latest cooled node — a digest near the live frontier is likelier relevant), and
// capped at `cap`. Overflow digest ids are returned separately (moreIDs) so they
// stay reachable via expand_digest even when not shown inline — cold_digests is the
// only exit for folded cold nodes. Shared by the current task overview and the
// read-only related-task overview (§2 cross-task reuse).
func coldDigestsRecent(store *db.ExplorationStore, cap int) (shown []map[string]any, moreIDs []int64) {
	ads, err := store.ActiveDigests()
	if err != nil || len(ads) == 0 {
		return nil, nil
	}
	type dg struct {
		id        int64
		entry     map[string]any
		freshness int64 // max member id (ids are monotonic ≈ creation time)
	}
	items := make([]dg, 0, len(ads))
	for _, d := range ads {
		var p struct {
			Body string `json:"body"`
		}
		_ = json.Unmarshal(d.Payload, &p)
		ms, _ := store.DigestMembers(d.ID) // sorted asc → last = freshest
		var fresh int64
		if len(ms) > 0 {
			fresh = ms[len(ms)-1]
		}
		items = append(items, dg{
			id:        d.ID,
			entry:     map[string]any{"id": d.ID, "body": p.Body, "member_count": len(ms)},
			freshness: fresh,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].freshness > items[j].freshness })
	for i, it := range items {
		if i < cap {
			shown = append(shown, it.entry)
		} else {
			moreIDs = append(moreIDs, it.id)
		}
	}
	return shown, moreIDs
}

// hiddenMembersFor returns a predicate telling whether a member is hidden (folded
// into an active digest AND still cold) in the given store — so a source task's
// overview folds exactly the way that task folds itself (§2 cross-task: "当前任务
// 什么展示逻辑，关联任务就什么逻辑"). A revived (now hot) covered member is NOT
// hidden (§6 render-time revival check). Returns a never-hidden predicate when the
// store has no digests.
func hiddenMembersFor(store *db.ExplorationStore) func(int64) bool {
	covered, err := store.CoveredMembers()
	if err != nil || len(covered) == 0 {
		return func(int64) bool { return false }
	}
	var hot map[int64]bool
	if cg, _, err := loadColdGraph(store); err == nil {
		hot = cg.hotSet()
	}
	return func(id int64) bool { _, c := covered[id]; return c && !hot[id] }
}

// resolveDigest finds a digest node by id in the current task, else in a direct
// source task (read-only, §2). Returns the node, its owning store, and the source
// task id (0 = current task).
func (t *ToolSet) resolveDigest(id int64) (*db.Node, *db.ExplorationStore, int64) {
	if n, _ := t.ts.GetNode(id); n != nil && n.Kind == db.KindDigest {
		return n, t.ts, 0
	}
	srcs, _ := t.ts.DirectSourceStores()
	for _, s := range srcs {
		if n, _ := s.Store.GetNode(id); n != nil && n.Kind == db.KindDigest {
			return n, s.Store, s.Task.TaskID
		}
	}
	return nil, nil, 0
}

// expandDigest returns a digest's covered members as a compact list (§6.1). It is
// a distinct tool from node_detail because it returns a LIST of members, not one
// node's full detail.
func (t *ToolSet) expandDigest() actool.CoreTool {
	return t.writeExpTool("expand_digest",
		"콜드 다이제스트 하나를 펼칩니다: 접혀 있던 구성원을 압축 목록으로 반환합니다(id/summary/state/confidence), 개요의 recent_facts/recent_done_intents 와 같은 형태입니다. 특정 항목의 전체 세부/증거는 node_detail(member_id) 를 사용하세요.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "integer", "description": "digest 노드 id(개요의 cold_digests 에서 옴)"},
			},
			"required": []any{"id"},
		},
		func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
			var in struct {
				ID int64 `json:"id"`
			}
			_ = json.Unmarshal(raw, &in)
			n, store, srcTaskID := t.resolveDigest(in.ID)
			if n == nil {
				return jsonResult(map[string]any{"error": fmt.Sprintf("#%d 은(는) digest 노드가 아닙니다(이 작업 또는 직접 연관 작업 어디에도 없음)", in.ID)})
			}
			var p struct {
				Body string `json:"body"`
			}
			_ = json.Unmarshal(n.Payload, &p)
			members, _ := store.DigestMembers(in.ID)
			list := make([]map[string]any, 0, len(members))
			for _, m := range members {
				entry := t.digestMemberEntry(store, m)
				if srcTaskID > 0 { // 关联任务的成员：只读，带继承标记（§2）
					entry["inherited"] = true
					entry["source_task_id"] = srcTaskID
				}
				list = append(list, entry)
			}
			out := map[string]any{
				"id":      in.ID,
				"state":   n.State, // active / superseded
				"body":    p.Body,
				"members": list,
			}
			if srcTaskID > 0 {
				out["inherited"] = true
				out["source_task_id"] = srcTaskID
			}
			return jsonResult(out)
		})
}
