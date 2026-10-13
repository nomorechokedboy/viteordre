{
	// The app is not currently linked to the encore.dev platform.
	// Use "encore app link" to link it.
	"id": "",

	// The web app calls this API from the browser with an Authorization header. Encore rejects
	// such cross-origin requests unless the origin is listed here, so add the production web
	// origin (for example "https://app.example.com") next to the local one.
	"global_cors": {
		"allow_origins_with_credentials": ["http://localhost:3000"],
		"allow_headers": ["Authorization"]
	}
}
