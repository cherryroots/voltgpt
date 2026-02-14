package wavespeed

const (
	baseURL string = "https://api.wavespeed.ai/api/v3"
)

const (
	BytedanceModels    ModelFamily   = "bytedance"
	WaveSpeedModels    ModelFamily   = "wavespeed-ai"
	AlibabaModels      ModelFamily   = "alibaba"
	WanT2V             ModelType     = "wan-2.5/text-to-video"
	WanI2V             ModelType     = "wan-2.5/image-to-video"
	SeedDreamModel     ModelType     = "seedream-v4"
	SeedDreamEditModel ModelType     = "seedream-v4/edit"
	SeedDanceV1_5Pro   ModelType     = "seedance-v1.5-pro"
	SeedDanceTemplate  ModelTemplate = "seedance-v1-%1-%2-%3"
	HunyuanVideoFoley  ModelType     = "hunyuan-video-foley"
)

const (
	SeedDancePro  SeedDanceVersion = "pro"
	SeedDanceLite SeedDanceVersion = "lite"
)

const (
	SeedDanceI2V SeedDanceType = "I2V"
	SeedDanceT2V SeedDanceType = "T2V"
)

const (
	SeedDance480p  SeedDanceResolution = "480p"
	SeedDance720p  SeedDanceResolution = "720p"
	SeedDance1080p SeedDanceResolution = "1080p"
)

type (
	ModelFamily         string
	ModelType           string
	ModelTemplate       string
	SeedDanceVersion    string
	SeedDanceType       string
	SeedDanceResolution string
)

type SeedDreamSubmissionRequest struct {
	Prompt       string  `json:"prompt"`
	Size         *string `json:"size,omitempty"`
	Seed         *int    `json:"seed,omitempty"`
	Base64Output *bool   `json:"enable_base64_output,omitempty"`
	SyncMode     *bool   `json:"enable_sync_mode,omitempty"`
}

type SeedDreamEditSubmissionRequest struct {
	Prompt       string    `json:"prompt"`
	Size         *string   `json:"size,omitempty"`
	Images       []*string `json:"images,omitempty"`
	Seed         *int      `json:"seed,omitempty"`
	Base64Output *bool     `json:"enable_base64_output,omitempty"`
	SyncMode     *bool     `json:"enable_sync_mode,omitempty"`
}

type SeedDanceT2VSubmissionRequest struct {
	Prompt      string  `json:"prompt"`                 // Text prompt for video generation; Positive text prompt; Cannot exceed 2000 characters
	AspectRatio *string `json:"aspect_ratio,omitempty"` // The aspect ratio of the generated video
	Duration    *int    `json:"duration,omitempty"`     // Generate video duration length seconds. 5-10 seconds
	Seed        *int    `json:"seed,omitempty"`         // The seed for random number generation.
}

type SeedDanceI2VSubmissionRequest struct {
	Prompt   *string `json:"prompt,omitempty"`   // Text prompt for video generation; Positive text prompt; Cannot exceed 2000 characters
	Image    string  `json:"image"`              // Input image for video generation; Supported image formats include .jpg/.jpeg/.png; The image file size cannot exceed 10MB, and the image resolution should not be less than 300*300px
	Duration *int    `json:"duration,omitempty"` // Generate video duration length seconds. 5-10 seconds
	Seed     *int    `json:"seed,omitempty"`     // The seed for random number generation.
}

type WanT2VSubmissionRequest struct {
	Prompt         string  `json:"prompt"`
	NegativePrompt *string `json:"negative_prompt,omitempty"`
	Audio          *string `json:"audio,omitempty"`
	Size           *string `json:"size,omitempty"`
	Duration       *int    `json:"duration,omitempty"`
	Seed           *int    `json:"seed,omitempty"`
}

type WanI2VSubmissionRequest struct {
	Image          string  `json:"image"`
	Audio          *string `json:"audio,omitempty"`
	Prompt         *string `json:"prompt,omitempty"`
	NegativePrompt *string `json:"negative_prompt,omitempty"`
	Resolution     *string `json:"resolution,omitempty"`
	Duration       *int    `json:"duration,omitempty"`
	Seed           *int    `json:"seed,omitempty"`
}

type HunyuanVideoFoleySubmissionRequest struct {
	Prompt *string `json:"prompt,omitempty"` // Text prompt for video generation; Positive text prompt; Cannot exceed 2000 characters
	Video  string  `json:"video"`            // Input image for video generation; Supported image formats include .jpg/.jpeg/.png; The image file size cannot exceed 10MB, and the image resolution should not be less than 300*300px
	Seed   *int    `json:"seed,omitempty"`   // The seed for random number generation.
}

type WaveSpeedQueryResult struct {
	ID string `json:"id"` // Task ID
}

type WaveSpeedResponse struct {
	Code    int    `json:"code"`    // HTTP status code (e.g., 200 for success)
	Message string `json:"message"` // Status message (e.g., “success”)
	Data    struct {
		ID      string   `json:"id"`      // Unique identifier for the prediction, Task ID
		Model   string   `json:"model"`   // Model ID used for the prediction
		Outputs []string `json:"outputs"` // Array of URLs to the generated content (empty when status is not completed)
		URLs    struct {
			Get string `json:"get"` // URL to retrieve the prediction result
		} `json:"urls"`
		HasNSFWContents []bool   `json:"has_nsfw_contents"` // Array of boolean values indicating NSFW detection for each output
		Status          string   `json:"status"`            // Status of the task: created, processing, completed, or failed
		CreatedAt       string   `json:"created_at"`        // ISO timestamp of when the request was created (e.g., “2023-04-01T12:34:56.789Z”)
		Error           string   `json:"error"`             // Error message (empty if no error occurred)
		Timings         struct { // Object containing timing details
			Inference int `json:"inference"` // Inference time in milliseconds
		} `json:"timings"`
	} `json:"data"`
}
