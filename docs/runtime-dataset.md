# Building the runtime dataset

Kaggle runs notebooks in its own container image and gives you no way to
supply one of your own. Anything GPUless needs that the Kaggle image does not
already have therefore arrives as a **dataset**, which Kaggle mounts read-only
under `/kaggle/input/<slug>` in seconds.

You build this once. After that a cold start is a mount, not a download.

## Layout

```
gpuless-runtime/
├── ComfyUI/            a ComfyUI checkout, custom nodes included
├── site-packages/      Python packages the Kaggle image lacks
└── bin/cloudflared     the tunnel client, x86-64
```

The runner script adds `site-packages/` to `PYTHONPATH` and starts
`ComfyUI/main.py`. It never writes inside the dataset: outputs, temp files and
the generated `extra_model_paths.yaml` all go to `/kaggle/working`.

## Building it

Do this on a machine with the same Python minor version as the Kaggle image
(3.11 at the time of writing), or the compiled wheels will not import.

```sh
mkdir -p gpuless-runtime/bin && cd gpuless-runtime

git clone --depth 1 https://github.com/comfyanonymous/ComfyUI
git clone --depth 1 <your-xtts-node-pack> ComfyUI/custom_nodes/xtts

pip install --target ./site-packages \
    -r ComfyUI/requirements.txt \
    -r ComfyUI/custom_nodes/xtts/requirements.txt

curl -L -o bin/cloudflared \
  https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64
chmod +x bin/cloudflared
```

Torch is already in the Kaggle image and is large. Leave it out unless you
need a specific version:

```sh
pip uninstall --target ./site-packages torch torchaudio torchvision 2>/dev/null || true
```

Then upload:

```sh
kaggle datasets init -p .
# edit dataset-metadata.json: set "title" and "id" to <your-user>/gpuless-runtime
kaggle datasets create -p . --dir-mode zip
```

Later updates are `kaggle datasets version -p . -m "note" --dir-mode zip`.

## Model datasets

One dataset per model, each mounted into the ComfyUI model folder it belongs
to. The panel wires them up:

| Panel setting | ComfyUI folder | Contents |
|---|---|---|
| Image model | `checkpoints` | `sd_xl_base_1.0.safetensors` or similar |
| Voice model | `tts` | the XTTS v2 weights and a speaker sample |

```sh
mkdir gpuless-sdxl && cd gpuless-sdxl
curl -L -o sd_xl_base_1.0.safetensors <weights-url>
kaggle datasets init -p . && kaggle datasets create -p . --dir-mode zip
```

The file name has to match what the workflow asks for. The built-in image
graph loads `sd_xl_base_1.0.safetensors`; either name yours that, or override
the workflow.

## Checking it works

Push a scratch notebook that lists the mount and imports the runtime:

```python
import os, sys
sys.path.insert(0, "/kaggle/input/gpuless-runtime/site-packages")
print(os.listdir("/kaggle/input/gpuless-runtime"))
import torch; print(torch.cuda.get_device_name(0))
```

If that prints a GPU name and the three directories, GPUless has what it
needs.

## Keeping it small

- Datasets are capped, and every gigabyte is mount time on each cold start.
- Skip anything the Kaggle image already ships: torch, numpy, pillow, requests.
- One model per dataset, so a change to one does not re-upload the others.
