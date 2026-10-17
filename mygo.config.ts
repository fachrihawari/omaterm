import { defineConfig } from "mygo-cli";

// oterm uses the native UI (github.com/egoist/mygo/ui): it has no web
// frontend, so the frontend fields of defineConfig are left out.
export default defineConfig({
  name: "oterm",
  identifier: "com.example.oterm",
  version: "0.1.0",
  out: "build",
});
