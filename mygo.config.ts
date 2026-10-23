import { defineConfig } from "mygo-cli";

// oterm uses the native UI (github.com/egoist/mygo/ui): it has no web
// frontend, so the frontend fields of defineConfig are left out.
export default defineConfig({
  name: "oterm",
  identifier: "com.example.oterm",
  version: "0.6.0",
  out: "build",
  macos: { minimumSystemVersion: "13.0" },
  // Signed updates: apps read the manifests of the repository's latest
  // release and install only what the key built into them signs. The
  // secret key is never here; `mygo build` reads it from
  // MYGO_UPDATER_PRIVATE_KEY, or from updates.privateKey.
  updates: {
    publicKey: "FaHunv4qiHNHes409Ko7lBU/Bc9Vqkha1W5t2Cg0br8=",
    github: "fachrihawari/omaterm",
  },
});
