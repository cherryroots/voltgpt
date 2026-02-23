# Wavespeed Model Descriptor Refactor

## Problem

Each Wavespeed model requires a dedicated `Send*Request` function in `request.go`. All of these functions are thin wrappers around `sendWaveSpeedRequest(family, modelType, payload)`. Adding a new model means touching both `structs.go` (new request struct + constants) and `request.go` (new Send function), with no structural guarantee that family and modelType are paired correctly.

## Goal

Make adding a new model a single-line change, and make the (family, modelType) relationship explicit at the definition site rather than scattered across function bodies.

## Design

### Model descriptor (`structs.go`)

Introduce a `Model` struct with unexported fields:

```go
type Model struct {
    family    ModelFamily
    modelType ModelType
}
```

All known models become package-level vars:

```go
var (
    SeedDream      = Model{BytedanceModels, SeedDreamModel}
    SeedDreamEdit  = Model{BytedanceModels, SeedDreamEditModel}
    WanText2Video  = Model{AlibabaModels, WanT2V}
    WanImage2Video = Model{AlibabaModels, WanI2V}
    HunyuanFoley   = Model{BytedanceModels, HunyuanVideoFoley}
)
```

SeedDance uses a constructor because its model type is built from three parameters:

```go
func SeedDance(version SeedDanceVersion, t SeedDanceType, res SeedDanceResolution) Model {
    return Model{BytedanceModels, ModelType(fmt.Sprintf("seedance-v1-%s-%s-%s", version, t, res))}
}
```

The unused `SeedDanceV1_5Pro` constant is removed. All other `ModelFamily`/`ModelType` string constants remain as building blocks for the var block.

### Single send function (`request.go`)

All 6 `Send*Request` functions collapse into one:

```go
func Send(model Model, payload any) (*WaveSpeedResponse, error) {
    return sendWaveSpeedRequest(model.family, model.modelType, payload)
}
```

`generateSeedDanceModelType` is removed — its logic moves into `SeedDance()`.

`QueryWaveSpeedResult`, `WaitForComplete`, `DownloadResult`, and `IsImageURL` are unchanged.

### Call site updates (`internal/handler/commands.go`)

| Before | After |
|--------|-------|
| `wave.SendSeedDreamEditRequest(req)` | `wave.Send(wave.SeedDreamEdit, req)` |
| `wave.SendSeedDreamRequest(req)` | `wave.Send(wave.SeedDream, req)` |
| `wave.SendWanI2VRequest(req)` | `wave.Send(wave.WanImage2Video, req)` |
| `wave.SendWanT2VRequest(req)` | `wave.Send(wave.WanText2Video, req)` |

`SendSeedDanceT2VRequest` and `SendSeedDanceI2VRequest` have no call sites and are simply removed.

## Adding a new model after this change

1. Add a request struct to `structs.go` if the model has unique parameters
2. Add one line to the `var` block: `NewModel = Model{SomeFamily, SomeModelType}`
3. Call `wave.Send(wave.NewModel, req)` at the call site

No new function in `request.go` required.
