# Memory Package Coverage Improvement

**Date:** 2026-02-22
**Current coverage:** 36.8%
**Target:** ~75%+ without token, higher with

## Approach

No production code changes. Extend `memory_test.go` with two categories of tests using the existing `setupTestDB` and `setupGemini` helpers.

## Category 1: Pure DB Tests (no token needed)

These functions are at 0% coverage despite having no Gemini dependency.

| Function | Test |
|---|---|
| `DeleteAllFacts` | Insert facts for two users, call DeleteAllFacts, verify TotalFacts=0 |
| `RefreshAllFactNames` | Insert users with facts, set preferred names, call RefreshAllFactNames, verify all prefixes updated |
| `GetRecentFacts` | Insert facts, call GetRecentFacts with limit, verify count and ordering |
| `findSimilarFacts` | Insert fact with zero embedding via insertFact, call findSimilarFacts with same zero vector, verify distance=0 returned; also verify facts beyond distanceThreshold are excluded |

## Category 2: Gemini-Gated Tests (skip without token, use setupGemini)

These test the end-to-end pipelines. All skip cleanly when `MEMORY_GEMINI_TOKEN` is unset.

| Function | Test |
|---|---|
| `consolidateAndStore` (no similar) | Embed a fact with no prior data → verify insertFact path |
| `consolidateAndStore` (REINFORCE) | Insert a fact, call consolidateAndStore with same fact rephrased → verify reinforcement_count increases |
| `consolidateAndStore` (INVALIDATE) | Insert a location fact, call consolidateAndStore with contradicting location → verify old fact deactivated, new one active |
| `consolidateAndStore` (MERGE) | Insert "owns Xbox", consolidate "bought PS5" → verify merged fact |
| `Retrieve` | Insert fact via consolidateAndStore, call Retrieve with related query → verify fact returned |
| `RetrieveGeneral` | Insert fact for user A, call RetrieveGeneral excluding user A → verify empty; including other users → verify returned |
| `RetrieveMultiUser` | Insert facts for two users, call RetrieveMultiUser → verify XML output contains both |
| `flushBuffer` | Call flushBuffer directly with a seeded buffer entry → verify facts extracted and stored |

## What stays unchanged

- `TestExtractFacts` and `TestDecideAction` remain as-is
- `setupGemini` and `setupTestDB` helpers unchanged
- No interface extraction, no production code modifications
