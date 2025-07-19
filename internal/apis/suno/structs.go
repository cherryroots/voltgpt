package suno

const (
	baseURL        string = "https://api.sunoapi.org/api/v1"
	genEndpoint    string = "/generate"
	statusEndpoint string = "/generate/record-info"
)

const (
	MusicGenModelV3_5 MusicModel = "V3_5"
	MusicGenModelV4   MusicModel = "V4"
	MusicGenModelV4_5 MusicModel = "V4_5"
)

const (
	ResponseCodeSuccess             ResponseCode = 200
	ResponseCodeInvalidParameters   ResponseCode = 400
	ResponseCodeUnauthorized        ResponseCode = 401
	ResponseCodeNotFound            ResponseCode = 404
	ResponseRateLimit               ResponseCode = 405
	ResponseCodeThemeOrPromptLong   ResponseCode = 413
	ResponseCodeInsufficientCredits ResponseCode = 429
	ResponseCodeSystemMaintenance   ResponseCode = 455
	ResponseCodeServerError         ResponseCode = 500
)

const (
	TaskTypeChirpV3_5 TaskType = "chirp-v3-5"
	TaskTypeChirpV4   TaskType = "chirp-v4"
)

const (
	OperationTypeGenerate     OperationType = "generate"
	OperationTypeExtend       OperationType = "extend"
	OperationTypeUploadCover  OperationType = "upload_cover"
	OperationTypeUploadExtend OperationType = "upload_extend"
)

const (
	Pending             TaskStatus = "PENDING"
	TextSuccess         TaskStatus = "TEXT_SUCCESS"
	FirstSuccess        TaskStatus = "FIRST_SUCCESS"
	Success             TaskStatus = "SUCCESS"
	CreateTaskFailed    TaskStatus = "CREATE_TASK_FAILED"
	GenerateAudioFailed TaskStatus = "GENERATE_AUDIO_FAILED"
	CallbackException   TaskStatus = "CALLBACK_EXCEPTION"
	SensitiveWordError  TaskStatus = "SENSITIVE_WORD_ERROR"
)

type (
	MusicModel    string
	TaskType      string
	OperationType string
	ResponseCode  int
	TaskStatus    string
)

type SunoData struct {
	ID             string `json:"id"`
	AudioURL       string `json:"audioUrl"`
	StreamAudioURL string `json:"streamAudioUrl"`
	ImageURL       string `json:"imageUrl"`
	Prompt         string `json:"prompt"`
	ModelName      string `json:"modelName"`
	Title          string `json:"title"`
	CreateTime     string `json:"createTime"`
	Duration       int    `json:"duration"`
}

type MusicGenerationRequest struct {
	Prompt       string     `json:"prompt,omitempty"`
	Style        string     `json:"style,omitempty"`
	Title        string     `json:"title,omitempty"`
	CustomMode   bool       `json:"customMode"`
	Instrumental bool       `json:"instrumental"`
	Model        MusicModel `json:"model"`
	NegativeTags string     `json:"negativeTags,omitempty"`
	CallBackUrl  string     `json:"callBackUrl"`
}

type MusicGenerationResponse struct {
	Code ResponseCode `json:"code"` // Response code
	Msg  string       `json:"msg"`  // Error message when code is not 200
	Data struct {
		TaskID string `json:"taskId"` // Task ID for the music generation request
	} `json:"data"`
}

type MusicGenerationDetails struct {
	TaskID string `json:"taskId"` //
}

type MusicGenerationDetailsResponse struct {
	TaskID        string `json:"taskId"`
	ParentMusicID string `json:"parentMusicId"`
	Param         string `json:"param"`
	Response      struct {
		TaskID   string     `json:"taskId"`
		SunoData []SunoData `json:"sunoData,omitempty"`
	} `json:"response"`
	Status        TaskStatus    `json:"status"`
	Type          TaskType      `json:"type"`
	OperationType OperationType `json:"operationType"`
	ErrorCode     ResponseCode  `json:"errorCode"`
	ErrorMessage  string        `json:"errorMessage"`
}
