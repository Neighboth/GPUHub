// Generates src/lib/providerIcons.generated.ts from simple-icons package (CC0 licensed).
import { writeFileSync } from "node:fs";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const si = require("simple-icons");

const MAP = {
  "kaggle": "siKaggle",
  "colab": "siGooglecolab",
  "lightning": "siLightning",
  "paperspace": "siPaperspace",
  "saturn": "siSaturn",
  "sagemaker-studio-lab": "siAmazonaws", // may be missing; falls back to monogram
  "paddle-aistudio": "siBaidu",
  "modelscope": "siAlibabacloud",
  "cloudstudio": "siTencenthy",
  "leafcloud": "siLeafcloud",
  "hf-spaces": "siHuggingface",
  "modal": "siModal",
  "beam": "siBeam",
  "intel-tiber": "siIntel",
  "genesis": "siGenesis",
  "anyscale": "siAnyscale",
  "jarvislabs": "siJarvislabs",
  "nebius": "siNebius",
  "hyperbolic": "siHyper",
  "salad": "siSalad",
  "runpod": "siRunpod",
  "codesphere": "siCodesphere",
  "replit": "siReplit",
  "deepnote": "siDeepnote",
  "hex": "siHex",
  "noteable": "siNoteable",
  "domino": "siDomino",
  "floydhub": "siFloydhub",
  "spell": "siSpell",
  "valohai": "siValohai",
  "determined": "siDetermined",
  "clearml": "siClearml",
  "runner": "siNvidia",
};

const out = {};
for (const [id, slug] of Object.entries(MAP)) {
  const icon = si[slug];
  if (!icon) { console.warn("missing:", slug); continue; }
  const m = icon.svg.match(/ d="([^"]+)"/);
  if (!m) { console.warn("no path for", slug); continue; }
  out[id] = { title: icon.title, hex: icon.hex, path: m[1] };
}

const body = `// AUTO-GENERATED from simple-icons (CC0) - do not edit by hand. Re-run scripts/genicons.mjs.
export interface SimpleIcon { title: string; hex: string; path: string }
export const PROVIDER_ICONS: Record<string, SimpleIcon> = ${JSON.stringify(out, null, 1)};
`;
writeFileSync("src/lib/providerIcons.generated.ts", body, "utf8");
console.log("wrote", Object.keys(out).length, "icons");
