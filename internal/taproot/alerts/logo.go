package alerts

// logoPath is the favicon mark as a 256x256 PNG, rendered by
// scripts/taproot/render-logo.ts. Discord fetches it by URL for the avatar
// and thumbnail, so alerts need no upload and work from a tailnet-only
// server. Messages already posted keep pointing at this path on main.
const logoPath = "internal/taproot/alerts/assets/taproot.png"

const logoURL = "https://raw.githubusercontent.com/265866/taproot/main/" + logoPath
