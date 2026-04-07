export type Client = {
  slug: string;
  name: string;
  description: string;
  status: "validated" | "alpha";
};

export const clients: Client[] = [
  {
    slug: "qbittorrent",
    name: "qBittorrent",
    description: "WebUI API v2 — qBittorrent 4.5+ and 5.x.",
    status: "validated",
  },
  {
    slug: "transmission",
    name: "Transmission",
    description: "RPC with the X-Transmission-Session-Id 409 dance.",
    status: "validated",
  },
  {
    slug: "deluge",
    name: "Deluge",
    description: "Web JSON-RPC at /json. auth.login + web.connect + core.add_torrent_*.",
    status: "validated",
  },
  {
    slug: "utorrent",
    name: "µTorrent",
    description: "Token-based WebUI flow. Mocked-server tested.",
    status: "alpha",
  },
  {
    slug: "downloadfolder",
    name: "Download folder",
    description: "Write the .torrent or .magnet to a folder. Pair with SABnzbd / NZBGet for Usenet.",
    status: "validated",
  },
];
