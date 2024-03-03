# buchhalter command line tool

buchhalter-cli is a command line tool to automatically download invoices from suppliers.
It uses the [open invoice collector database](https://github.com/oicdb/oicdb-repository) 
by default but you can use the --dev mode to overwrite recipes for a specific provider with your local ones.  

## Installation
You can install the buchhalter-cli by cloning the repository and running the main.go file.
We also plan to provide a brew package in the future (WIP).

```bash
git clone git@github.com:buchhalter-ai/buchhalter-ai-cli.git
cd buchhalter-ai-cli
```

## Usage
buchhalter-cli is a command line tool to interact with the buchhalter-ai API.

### 1.**Tagging**
Tag all credentials you want to use in 1password with "buchhalter-ai" and make sure that every credential 
has the url field filled with the correct url of the supplier (e.g. the login url). 

### 2.**Login**
Login to your 1password vault in the console with: ```eval $(op signin)```

### 3.**Sync**
Sync all latest invoices from all tagged suppliers in 1password to your local disk: 
```go run main.go sync```

Example: Load latest invoices from Hetzner Cloud only (using the default recipe from oicdb.org):
```
go run main.go sync hetzner
```

## Local invoice storage
All invoices will be stored in a folder called "buchhalter" in your users folder by default (e.g. /Users/bernd/buchhalter).
You can place local oicdb recipes (for testing or modifications) in the _local/recipes subfolder of your buchhalter directory. 
You can use the --dev flag to overwrite recipes for a specific provider with your local ones.

Example: Load all invoices from Hetzner Cloud (using your local recipe stored in buchhalter/_local/recipes/hetzner.json):
```
go run main.go sync hetzner --dev
```

That's it! You can now use buchhalter cli to automatically download all your invoices from your suppliers.
Have fun and feel free to create a lot of pull requests with new recipes for our oicdb.org database. 
We're looking forward to your contributions!

## How does it work?
1. buchhalter cli reads all tagged credentials from your 1password vault.
2. buchhalter cli loads all recipes from the central open invoice collector database by default.
3. buchhalter cli mapps credentials with recipes and uses the credentials to login to the suppliers website and download the invoices.
4. Optional (when --dev mode not active): Send anonymous usage data to the buchhalter-ai API to improve the used recipes and the tool.

## Privacy
1. buchhalter cli runs locally on your mac and will only read tagged credentials (username, password, url, 2FA-Code) from your 1password vault. 
2. buchhalter cli will never write to your 1password vault.
3. buchhalter cli will never store any credentials or data on your local machine.
4. buchhalter cli loads recipes from the open invoice collector database by default.
5. buchhalter cli will never send any data to the buchhalter-ai API without your consent.
