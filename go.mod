module spore

go 1.25

require (
	github.com/google/go-github/v60 v60.0.0
	github.com/joho/godotenv v1.5.1
	github.com/matiasinsaurralde/go-e2b v0.0.0-20260519075826-78b76f7b2894
	github.com/slack-go/slack v0.15.0
	golang.org/x/oauth2 v0.30.0
)

require (
	connectrpc.com/connect v1.19.1 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/gorilla/websocket v1.4.2 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/matiasinsaurralde/go-e2b => ./internal/go-e2b
