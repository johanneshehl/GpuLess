# Building the datasets

Kaggle runs notebooks in its own container image and gives you no way to supply one of your own. Anything GPUless
needs that the Kaggle image does not already have therefore arrives as a **dataset**, which Kaggle mounts read-only
under `/kaggle/input/<slug>` in seconds.

You build these once. After that a cold start is a mount, not a download.

| Dataset | Panel setting | Contents |
|---|---|---|
| `gpuless-runtime` | Runtime dataset (required) | ComfyUI, extra Python packages, cloudflared |
| `gpuless-sdxl` | Image model | `sd_xl_base_1.0.safetensors` |
| `gpuless-xtts` | Voice model | XTTS v2 weights and a speaker sample |

## Build them on Kaggle (recommended)

The easiest way is to let Kaggle build the datasets itself. The packages then match the image the runner uses, and
nothing large passes through your own connection. The two builder notebooks are in
[`docs/notebooks`](notebooks).

For each notebook:

1. On kaggle.com, click **Create > New Notebook**, then **File > Import Notebook** and upload the `.ipynb` file.
2. In the settings panel on the right, set **Accelerator** to **None** and switch **Internet** on. The builders need
   no GPU and spend none of your GPU hours. Internet access needs a phone-verified account, which GPU use needs as
   well.
3. Click **Save Version > Save & Run All (Commit)** and wait until the version has finished. The runtime takes about
   10 minutes, the image model a few minutes for the 7 GB download.
4. Open the finished version, go to its **Output** and create a new dataset from it. Keep it private and give it the
   name from the table above.
5. In the GPUless setup, enter the dataset as `your-kaggle-username/gpuless-runtime` (and `.../gpuless-sdxl`).

| Notebook | Builds |
|---|---|
| [`build-runtime.ipynb`](notebooks/build-runtime.ipynb) | `gpuless-runtime`: clones ComfyUI, asks pip which packages the Kaggle image is missing and adds only those, downloads cloudflared and runs a ComfyUI self-test |
| [`build-sdxl.ipynb`](notebooks/build-sdxl.ipynb) | `gpuless-sdxl`: downloads Stable Diffusion XL base 1.0 from Hugging Face |

To update the runtime later, run the builder again and create a new version of the same dataset from the output.

## Layout

The runner expects this layout at the top level of the runtime dataset:

```
ComfyUI/            a ComfyUI checkout, custom nodes included
site-packages/      Python packages the Kaggle image lacks
bin/cloudflared     the tunnel client, x86-64
```

The runner adds `site-packages/` to `PYTHONPATH` and starts `ComfyUI/main.py`. It never writes inside the dataset:
outputs, temp files and the generated `extra_model_paths.yaml` all go to `/kaggle/working`.

Model datasets are mounted into the ComfyUI model folder they belong to:

| Panel setting | ComfyUI folder | Contents |
|---|---|---|
| Image model | `checkpoints` | `sd_xl_base_1.0.safetensors` or similar |
| Voice model | `tts` | the XTTS v2 weights and a speaker sample |

The file name has to match what the workflow asks for. The built-in image graph loads `sd_xl_base_1.0.safetensors`;
either name yours that, or override the workflow.

## Voice

Text to speech is not part of ComfyUI. The built-in voice workflow expects an XTTS node pack with the node classes
`XTTSLoader` and `XTTSGenerate`, and node packs name their classes differently. Before you set up the voice model,
pick a node pack, clone it into `ComfyUI/custom_nodes` in the runtime builder, and adapt `voice.json` to its class
names through the workflow override directory. Until then, leave the voice model empty and the Voice tab stays
hidden.

## Building by hand

If you prefer to build the runtime on your own machine, use the same Python minor version as the Kaggle image, or
the compiled wheels will not import. Leave out torch, which the image already has and which is several gigabytes.

```sh
mkdir -p gpuless-runtime/bin && cd gpuless-runtime
git clone --depth 1 https://github.com/comfyanonymous/ComfyUI
pip install --target ./site-packages -r ComfyUI/requirements.txt
rm -rf site-packages/torch* site-packages/nvidia* site-packages/triton*
curl -L -o bin/cloudflared \
  https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64
chmod +x bin/cloudflared

kaggle datasets init -p .
# edit dataset-metadata.json: set "title" and "id" to <your-user>/gpuless-runtime
kaggle datasets create -p . --dir-mode zip
```

## Keeping it small

- Every gigabyte is mount time on each cold start.
- Skip anything the Kaggle image already ships: torch, numpy, pillow, requests. The builder notebook does this for
  you.
- One model per dataset, so a change to one does not re-upload the others.
