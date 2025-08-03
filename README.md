# Web Video

Simple self-hosted video website with metadata driven entirely from JSON. No code changes needed to add/remove videos.

## Overview

- Backend: Go
- Frontend: HTML template + local video files
- Video list: `videos.json`
- Static assets: served from `static/`

## Requirements

- Go 1.21+ installed
- Project layout (must run from repo root or adjust paths via flags):

```

.
├── main.go
├── videos.json
├── templates/
│   └── index.html
└── static/
├── css/
├── images/
└── videos/

````

## Build & Run

Run the server with sensible defaults:

```sh
go run main.go
````

Flags (override defaults):

* `-address` : bind address (default `:8080`)
* `-videos` : path to videos JSON file (default `videos.json`)
* `-static` : static assets directory (default `static`)
* `-templates` : templates directory (default `templates`)
* `-title` : page title shown in header

Example:

```sh
go run main.go -address=":9090" -videos="videos.json" -static="static" -templates="templates" -title="Freedom Unleashed"
```

## Video Metadata (`videos.json`)

The site reads this file at startup. Format:

```json
[
  {
    "title": "Freedom Unleashed",
    "description": "A montage of pure, unadulterated American freedom.",
    "fileName": "video1.mp4"
  },
  {
    "title": "Eagle's Flight",
    "description": "Soaring high, without a care in the world.",
    "fileName": "video2.mp4"
  }
]
```

* `fileName` must be the base name (no path traversal) and the file must exist under `static/videos/`.
* Supported video delivery is via standard HTML5 `<video>`; expected container is MP4 (adjust template if adding other
  types).

## Validation (manual, immediate)

Before starting, ensure the JSON is syntactically valid and entries have required fields:

```sh
jq -e 'map(select(.title and .description and .fileName)) | length > 0' videos.json
```

If that returns non-zero exit status, the file is malformed or missing required fields.

Optionally check that all referenced video files exist:

```sh
python - <<'PY'
import json, os
with open("videos.json") as f:
    videos = json.load(f)
missing = []
for video in videos:
    filename = video.get("fileName","")
    if not filename or not os.path.isfile(os.path.join("static", "videos", os.path.basename(filename))):
        missing.append(filename)
if missing:
    print("Missing video files:", missing)
    exit(1)
print("All referenced video files present.")
PY
```

*(Replace with your preferred tooling; that script is illustrative and can be adapted.)*

## Static Content

* Place video files in `static/videos/`
* Styles are in `static/css/style.css`
* Header image (if any) lives under `static/images/`; template uses
  `/static/images/Gemini_Generated_Image_v8dvicv8dvicv8dv.jpg` — replace or remove as needed.

## Title

Default page title is:
`I am an American and I don’t give a fuck!`

Override via `-title` flag.

## License

MIT. See `LICENSE` for full text.


