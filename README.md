# buchhalter command line tool

buchhalter-cli is a command line tool (CLI) that downloads invoices from suppliers automatically.
It uses the [open invoice collector database](https://github.com/oicdb/oicdb-repository) (OICDB) by default.

## Installation

### Pre-Compiled Binary

Download the pre-compiled binary from our [Release section](https://github.com/buchhalter-ai/buchhalter-ai-cli/releases).

### Homebrew

```sh
$ brew tap buchhalter-ai/buchhalter
$ brew install buchhalter-ai
$ buchhalter version
```

### From Source code

Install the buchhalter-cli by cloning the repository and running the main.go file.

```sh
$ git clone git@github.com:buchhalter-ai/buchhalter-ai-cli.git
$ cd buchhalter-ai-cli
$ make build
$ bin/buchhalter version
```

## Usage

buchhalter-cli is a command line tool to interact with the buchhalter-ai API.

### 1.**Tagging**

Tag all credentials you want to use in 1Password with `buchhalter-ai` and make sure that every credential
has the URL field been filled with the supplier's correct URL (e.g., the login URL).

### 2.**Login**

Login to your 1Password vault in the console with: `eval $(op signin)`

### 3.**Sync**

#### From all suppliers

Sync all the latest invoices from all tagged suppliers in 1Password to your local disk:

```sh
buchhalter sync
```

#### From one supplier

Example: Load the latest invoices from Hetzner Cloud only (using the default recipe from [oicdb.org](https://oicdb.org/)):

```sh
buchhalter sync hetzner
```

## Configuration

The configuration file `~/.buchhalter/.buchhalter.yaml` will be automatically created on startup.
The following settings are available for configuration:

| Setting                                     | Type   | Default                      | Description                                                                                                                                                                                                                                                                                                                       |
|---------------------------------------------|--------|------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `credential_provider_cli_command`           | String |                              | Path to the Password Manager CLI binary (e.g. `/usr/local/bin/op` for 1Password). If not configued, the binary will be automatically detected on the systems `$PATH`.                                                                                                                                                             |
| `credential_provider_item_tag`              | String | `buchhalter-ai`              | Name of the item tag buchhalter-cli will query. Only items with this particular tag are considered. Useful to limit the scope. If empty, buchhalter-cli will query all items in your vault. For 1Password, see [Organize with favorites and tags](https://support.1password.com/favorites-tags/)                                  |
| `buchhalter_directory`                      | String | `~/buchhalter/`              | Directory to store the invoices from suppliers into.                                                                                                                                                                                                                                                                              |
| `buchhalter_max_download_files_per_receipt` | Int    | `2`                          | Download only the latest 2 invoices per receipt and ignore the rest. `0` means all invoices.                                                                                                                                                                                                                                      |
| `buchhalter_config_directory`               | String | `~/.buchhalter/`             | Directory to store the buchhalter configuration.                                                                                                                                                                                                                                                                                  |
| `buchhalter_api_host`                       | String | `https://app.buchhalter.ai/` | HTTP Host for the Buchhalter API.                                                                                                                                                                                                                                                                                                 |
| `buchhalter_always_send_metrics`            | Bool   | `false`                      | Activate / deactivate sending usage metrics to Buchhalter API.                                                                                                                                                                                                                                                                    |
| `dev`                                       | Bool   | `false`                      | Activate / deactivate development mode for _buchhalter-cli_ (without updates and sending metrics).                                                                                                                                                                                                                                |

The configuration file is in YAML format.
An example looks like:

```yaml
credential_provider_cli_command: "/opt/homebrew/bin/op"
credential_provider_item_tag: "invoice"
buchhalter_always_send_metrics: True
```

## Command line arguments and flags

All command line arguments and flags are available via `buchhalter --help`:

```sh
Usage:
  buchhalter [command]

Available Commands:
  connect     Connects to the Buchhalter Platform and verifies your premium membership
  help        Help about any command
  sync        Synchronize all invoices from your suppliers
  version     Output the version info

Flags:
  -d, --dev    development mode (e.g. without OICDB recipe updates and sending metrics)
  -h, --help   help for buchhalter
  -l, --log    log debug output

Use "buchhalter [command] --help" for more information about a command.
```

The `--dev` flag enables the development mode.
In this mode particular activities are skipped like checking the buchhalter api for a new version of OICDB invoice recipes or the transfer of usage metrics to the buchhalter API.

The `--log` flag will write a activities into a log file placed at `<buchhalter_directory>/buchhalter-cli.log` (default: `~/buchhalter/buchhalter-cli.log`).

## Local invoice storage

By default, all invoices are stored in a folder called "buchhalter" in your users' folder (e.g. `/Users/bernd/buchhalter`).
You can place local oicdb recipes (for testing or modifications) in the `_local/recipes` subfolder of your buchhalter directory.
You can use the `--dev` flag to overwrite recipes for a specific supplier with your local ones.

Example: Load all invoices from Hetzner Cloud (using your local recipe stored in `buchhalter/_local/recipes/hetzner.json`):

```sh
buchhalter sync hetzner --dev
```

That's it! You can now use buchhalter-cli to download all your invoices from your suppliers automatically.
Have fun, and feel free to create a lot of pull requests with new recipes for our oicdb.org database.
We're looking forward to your contributions!

## How does it work?

1. buchhalter-cli reads all tagged credentials from your 1Password vault.
2. buchhalter-cli loads all recipes from the central open invoice collector database by default.
3. buchhalter-cli maps credentials with recipes and uses the credentials to log in to the supplier's website and download the invoices.
4. Optional (when `--dev` mode is not active): Send anonymous usage data to the buchhalter-ai API to improve the used recipes and the tool.

## Privacy

1. buchhalter-cli runs locally on your Mac and will only read tagged credentials (username, password, url, 2FA-Code) from your 1Password vault.
2. buchhalter-cli will never write to your 1Password vault.
3. buchhalter-cli will never store credentials or data on your local machine.
4. buchhalter-cli loads recipes from the open invoice collector database by default.
5. buchhalter-cli will never send any data to the buchhalter-ai API without your consent.

## Development

### Creating a new release

Imagine we want to release v1.2.3

```sh
$ git tag -a v1.2.3
$ git push origin v1.2.3
```

This triggers the automatic workflow to compile + create a new release + update the homebrew recipe.
