# flyover-reverse-engineering (2025 fork)

Export textured 3D models from Apple Maps **Flyover** (the 3D satellite mode) as Wavefront `.obj` files, given a latitude/longitude.

This is a fork of [retroplasma/flyover-reverse-engineering](https://github.com/retroplasma/flyover-reverse-engineering). The original work figured out Apple's manifest bootstrap, URL authentication, geo→tile math, the octree tile lookup, and the C3M mesh decompression (Huffman tables + an edgebreaker variant). All of that still holds — but Apple changed several things on the wire since the original was written, so the original code no longer produces a model out of the box. **This fork updates the pipeline to work with Apple's current data** and switches the project to Go modules.

> Research / educational / interoperability project. It talks to Apple's servers and uses Apple's data; respect Apple's terms and don't redistribute the downloaded geometry or imagery. No tokens, keys, or downloaded tiles are included in this repo.

---

## What changed vs. the original

If you used the original and got nothing but errors, this is why. Apple changed four things; all are handled here:

| Area | Change | How this fork handles it |
|------|--------|--------------------------|
| **Altitude manifest** | The resource manifest no longer ships `cache_base_url`, and the standalone altitude XML URL now 404s. The tool can no longer download the altitude manifest by itself. | **You must provide the `altitude-*.xml` file locally** (see below). |
| **Tile index (C3MM v1)** | The part-based octree metadata (`style=14`) used to decide which tiles exist is no longer served (404). | The octree lookup was removed. Tiles are now discovered by requesting the C3M mesh tiles directly and skipping empty responses. |
| **C3M version byte** | The mesh container's version is read from `data[3]`; Apple changed the adjacent flag byte `data[4]` (`0x03` → `0x07`), which broke the old version dispatch. | Version detection now reads the correct byte. The mesh format itself is unchanged. |
| **Textures** | Tile textures switched from JPEG to **HEIC**. | The parser accepts HEIC, and the exporter transcodes it to JPEG so the `.obj`/`.mtl` is viewable in standard tools. |

---

## ⚠️ The altitude file is no longer in Apple's data — you must supply it

Apple stopped including the altitude-manifest reference (`cache_base_url`) in the resource manifest, so this tool **cannot fetch the altitude manifest on its own anymore**. The altitude manifest is what maps a coordinate to a Flyover region/version, so it's required.

You need to provide the file yourself, once:

1. On a Mac that has opened Apple Maps at least once, grab the altitude manifest from the GeoServices cache:
   ```
   ~/Library/Caches/GeoServices/Resources/altitude-*.xml
   ```
2. Copy it into this project's cache directory:
   ```bash
   mkdir -p cache
   cp ~/Library/Caches/GeoServices/Resources/altitude-*.xml cache/
   ```

The tool reads the altitude manifest from `cache/` when it's present. The filename must match the one the current resource manifest references (e.g. `altitude-1426.xml`); if the names differ, the tool will tell you which file it expected — just rename your copy to match.

This file is plain region data and is platform-independent: once you have it, the rest of the pipeline (fetching, meshing, texturing) needs no Mac. See **Running on Linux / Windows** below.

---

## Requirements

- [Go](https://go.dev/) (module mode; tested with current Go)
- A HEIC→JPEG converter for textures:
  - macOS: `sips` (built in) — used by default
  - Linux/Windows: ImageMagick (built with libheif), `heif-convert` (libheif), or `ffmpeg`
- Optional: Node.js, to center/scale the output for Blender (`scripts/center_scale_obj.js`)

## Setup

### 1. Configuration (`config.json`)

```bash
cp config.example.json config.json
```

Then fill in two values:

- `resourceManifestURL` — Apple's GeoServices resource-manifest bootstrap URL.
- `tokenP1` — the static URL-authentication token.

Both are static values baked into Apple's `GeoServices` framework. **They are not included here** — extract them from your own system:

- The original repo's helper scripts (`scripts/get_config.sh`, `scripts/get_config_macos.sh`) extract them from the `GeoServices` binary.
- On recent macOS the framework binary is no longer a standalone file — it lives inside the dyld shared cache (`/System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld/`), where both strings can still be recovered.

`config.json` is git-ignored so your token never gets committed.

### 2. Altitude file

See [the altitude section above](#️-the-altitude-file-is-no-longer-in-apples-data--you-must-supply-it) — copy your `altitude-*.xml` into `cache/`.

## Usage

Export an area to `./downloaded_files/obj/...`:

```
go run cmd/export-obj/main.go [lat] [lon] [zoom] [tryXY] [tryH] [--parallel]

  Name    Description       Example
  ----------------------------------------
  lat     Latitude          41.8902
  lon     Longitude         12.4923
  zoom    Zoom (~13–20)     20
  tryXY   Area scan (±tiles per axis)   3
  tryH    Altitude scan (height indices)  40
```

Example — the Colosseum in Rome:

```bash
go run cmd/export-obj/main.go 41.8902 12.4923 20 3 40 --parallel
```

`tryXY` controls the size of the square tile grid: the grid is `(2·tryXY + 1)` tiles per side. At zoom 20 each tile is roughly ~28 m on the ground, so `tryXY=3` covers ~200 m across. `tryH` is how many height indices to probe per tile; in practice only the low indices contain data.

### Output

You get `exp_model.obj`, `exp_model.mtl`, and the JPEG textures. Vertices are in **ECEF** (Earth-Centered, Earth-Fixed) coordinates, so in a fresh Blender scene the model sits ~6,000 km from the origin and lies "on its side." To make it origin-centered and scaled for Blender:

```bash
node scripts/center_scale_obj.js   # writes exp_model.2.obj next to the original
```

Import the `*.2.obj` and switch the viewport to **Material Preview** to see the textures. (ECEF "up" is a tilted axis, so you may still want to rotate the object level.)

## Running on Linux / Windows

The Go program is pure Go — authentication, manifest parsing, mesh decompression, and networking all run on any OS, and the auth is not device-bound. To run off a Mac you need three things, none of which require a Mac at run time once you have them:

1. `config.json` with your two values (static; extract once).
2. The `altitude-*.xml` file in `cache/` (copy once from a Mac).
3. A non-`sips` HEIC→JPEG converter (ImageMagick / `heif-convert` / `ffmpeg`) — swap it in for the `sips` call in `pkg/fly/exp/obj.go`.

## Project layout

| Directory | Description |
|-----------|-------------|
| [cmd](./cmd) | command-line programs (`export-obj`, `auth`, `parse-c3m`, `parse-c3mm`) |
| [pkg](./pkg) | the actual library code (auth, manifest, C3M/C3MM, OBJ export) |
| [proto](./proto) | protobuf definitions |
| [scripts](./scripts) | config extraction + OBJ center/scale helpers |

## Credits

Original reverse-engineering and code: [retroplasma/flyover-reverse-engineering](https://github.com/retroplasma/flyover-reverse-engineering). Related project for Google Earth: [retroplasma/earth-reverse-engineering](https://github.com/retroplasma/earth-reverse-engineering). This fork only adapts the existing pipeline to Apple's current data format.

## Disclaimer

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
