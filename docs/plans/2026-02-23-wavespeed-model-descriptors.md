# Wavespeed Model Descriptor Refactor Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace 6 per-model `Send*Request` functions with a single `Send(model Model, payload any)` function, making adding a new model a one-line change.

**Architecture:** Introduce a `Model` struct pairing `ModelFamily` + `ModelType`. All known models become package-level `var`s. A `SeedDance()` constructor handles the three-parameter variant. One `Send` function replaces all per-model wrappers.

**Tech Stack:** Go, `internal/apis/wavespeed` package, `internal/handler/commands.go` call sites.

---

### Task 1: Add `Model` type and model vars to `structs.go`

**Files:**
- Modify: `internal/apis/wavespeed/structs.go`

**Step 1: Add `import "fmt"` at the top of `structs.go`**

Add a standard import block after `package wavespeed`:

```go
import "fmt"
```

**Step 2: Remove unused `SeedDanceV1_5Pro` constant and restore `HunyuanVideoFoley`**

Replace the second `const` block (lines 7-16) with:

```go
const (
	BytedanceModels    ModelFamily = "bytedance"
	WaveSpeedModels    ModelFamily = "wavespeed-ai"
	AlibabaModels      ModelFamily = "alibaba"
	WanT2V             ModelType   = "wan-2.5/text-to-video"
	WanI2V             ModelType   = "wan-2.5/image-to-video"
	SeedDreamModel     ModelType   = "seedream-v4"
	SeedDreamEditModel ModelType   = "seedream-v4/edit"
	HunyuanVideoFoley  ModelType   = "hunyuan-video-foley"
)
```

`SeedDanceV1_5Pro` is removed (never wired to a Send function). `HunyuanVideoFoley` is re-added (was dropped by the linter) — needed for the `HunyuanFoley` model var below.

**Step 3: Add `Model` struct after the existing type block**

After the `type (...)` block at line 34-40, add:

```go
// Model identifies a Wavespeed API endpoint as a (family, modelType) pair.
// Use the predefined vars (SeedDream, WanText2Video, etc.) or SeedDance() to construct one.
type Model struct {
	family    ModelFamily
	modelType ModelType
}
```

**Step 4: Add model vars and `SeedDance` constructor after the struct**

```go
var (
	SeedDream      = Model{BytedanceModels, SeedDreamModel}
	SeedDreamEdit  = Model{BytedanceModels, SeedDreamEditModel}
	WanText2Video  = Model{AlibabaModels, WanT2V}
	WanImage2Video = Model{AlibabaModels, WanI2V}
	HunyuanFoley   = Model{BytedanceModels, HunyuanVideoFoley}
)

// SeedDance constructs a Model for the SeedDance family.
// Version: SeedDancePro or SeedDanceLite
// T: SeedDanceT2V or SeedDanceI2V
// Resolution: SeedDance480p, SeedDance720p, or SeedDance1080p
func SeedDance(version SeedDanceVersion, t SeedDanceType, resolution SeedDanceResolution) Model {
	return Model{BytedanceModels, ModelType(fmt.Sprintf("seedance-v1-%s-%s-%s", version, t, resolution))}
}
```

**Step 5: Verify it builds**

```bash
/usr/local/go/bin/go build ./internal/apis/wavespeed/...
```

Expected: no output, exit 0.

---

### Task 2: Replace 6 `Send*` functions with `Send` in `request.go`

**Files:**
- Modify: `internal/apis/wavespeed/request.go`

**Step 1: Remove `generateSeedDanceModelType`**

Delete lines 17-20 (the `generateSeedDanceModelType` function). Its logic now lives in `SeedDance()` in structs.go.

**Step 2: Replace all 6 `Send*Request` functions with `Send`**

Delete lines 86-117 (all six `Send*Request` exported functions) and replace with:

```go
// Send submits a request to the Wavespeed API for the given model.
func Send(model Model, payload any) (*WaveSpeedResponse, error) {
	return sendWaveSpeedRequest(model.family, model.modelType, payload)
}
```

`sendWaveSpeedRequest` (lowercase) remains as the private HTTP implementation — only its single exported wrapper changes.

**Step 3: Verify it builds**

```bash
/usr/local/go/bin/go build ./internal/apis/wavespeed/...
```

Expected: no output, exit 0. (commands.go will now fail to compile — that's expected until Task 3.)

---

### Task 3: Update call sites in `commands.go`

**Files:**
- Modify: `internal/handler/commands.go`

There are four call sites. Make each substitution:

| Line (approx) | Before | After |
|---|---|---|
| ~138 | `wave.SendSeedDreamEditRequest(req)` | `wave.Send(wave.SeedDreamEdit, req)` |
| ~154 | `wave.SendSeedDreamRequest(req)` | `wave.Send(wave.SeedDream, req)` |
| ~274 | `wave.SendWanI2VRequest(req)` | `wave.Send(wave.WanImage2Video, req)` |
| ~290 | `wave.SendWanT2VRequest(req)` | `wave.Send(wave.WanText2Video, req)` |

**Step 1: Make all four substitutions**

Use search-and-replace. No logic changes — only the function call expression changes at each site.

**Step 2: Full build**

```bash
/usr/local/go/bin/go build ./...
```

Expected: no output, exit 0.

**Step 3: Run all tests**

```bash
/usr/local/go/bin/go test ./... -timeout 60s
```

Expected: all packages pass. No wavespeed-specific tests exist, so this validates the call sites didn't break the handler package.

**Step 4: Commit**

```bash
git add internal/apis/wavespeed/structs.go internal/apis/wavespeed/request.go internal/handler/commands.go
git commit -m "refactor(wavespeed): replace per-model Send functions with Model descriptor"
```
