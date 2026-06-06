# Boundary Contracts

Boundary files documented here:

- packages/client/src/bridge.ts
- packages/client/src/ws/client.ts
- packages/server-go/ws/handler.go
- packages/server-go/remote/pairing.go
- packages/server-go/remote/tls.go
- packages/shared/src/index.ts

Shared protocol contracts exposed from packages/shared/src/index.ts include:

- Session
- Message
- ToolCall
- FileNode
- FileContent
- ApprovalRequest
- SearchResult
- RuntimeConfig
- LlamaFleetScanResult
- DiscordConfig
- DiscordValidationResult
- DiscordMessageRecord
- DiscordSearchHit
- GitStatus
- GitCommit
- WorkspaceTask
- UsageTotals
- UsageModelBreakdown
- UsageProjectBreakdown
- UsageDashboard
- QualityIssue
- QualityReport
- QualityScanProgress
- TerminalInfo

Bridge-facing contracts include:

- InspectedPath
- BackgroundServiceStatus
- RemoteConfig
- DiscordBridge
- pairingCodeGenerator