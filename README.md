# Hack The Box Stats Widget

A lightweight AWS Lambda + DynamoDB backend paired with an embeddable front‑end widget (in `site_widget.html`) to display your Hack The Box statistics (owns, bloods, global & local rank) for today’s data cached in DynamoDB to minimize calls to the HTB API.

---

## Features

- **AWS Lambda** (Python) handler:
  - Checks DynamoDB for today’s cached HTB data.
  - On cache miss, fetches fresh data from the HTB API.
  - Caches the result in DynamoDB, then returns it.
- **DynamoDB caching** ensures at most one HTB API call per calendar day.
- **Embeddable widget** (`site_widget.html`):
  - Drop it into any HTML page.
  - Fetches your Lambda’s Function URL and renders stats in a centered “card” layout.
  - Customizable country‑rank label via a single JavaScript constant.

---

### DynamoDB Caching

To be a good API citizen and avoid hammering Hack The Box’s servers, the Lambda function uses a simple one‑day cache in DynamoDB:

1. **Partition by date**  
   Each record’s primary key is `date = YYYY‑MM‑DD`. On each invocation, the Lambda does a `GetItem` for today’s key.

2. **Cache hit**
   - If an item exists, the function returns it immediately (stripping out the `date` attribute).
   - **No HTB API call** → fast responses & minimal load on HTB.

3. **Cache miss**
   - The function fetches fresh data from the HTB API.
   - After a successful fetch, it writes the new stats back under today’s key.
   - Subsequent calls for the rest of the day reuse the cached entry.

4. **Rate‑limit friendly**  
   This design guarantees **at most one** HTB API call per calendar day, no matter how often you load the widget.

---

## Prerequisites

1. **AWS Account** with permissions to:
   - Create & deploy Lambda functions
   - Create & read/write a DynamoDB table
2. **Hack The Box** account with:
   - **User ID** (numeric)
   - **API token** (App Token from your [HTB profile](https://app.hackthebox.com/profile/settings))

---

## Backend Setup

1. **Create the DynamoDB Table**
   - Table name: your choice (e.g. `HTBStatsCache`)
   - Primary key:
     - **Partition key**: `date` (String)

2. **Package & Deploy the Lambda**
   ```bash
   # from your project root
   pip install requests -t ./package
   cd package
   zip -r ../lambda_function.zip .
   cd ..
   zip -g lambda_function.zip lambda_function.py
   ```

- In the AWS Console (or CLI), create/update a Lambda function:
  - Runtime: **Python 3.9+**
  - Handler: `lambda_function.lambda_handler`
  - Upload `lambda_function.zip`

3. **Configure Environment Variables**

   | Variable     | Description               | Example                                |
   | ------------ | ------------------------- | -------------------------------------- |
   | `TABLE_NAME` | DynamoDB table name       | `HTBStatsCache`                        |
   | `USER_ID`    | Your HTB numeric user ID  | `123456`                               |
   | `TOKEN`      | Your HTB API bearer token | `abcdef12-3456-7890-abcd-ef1234567890` |

4. **IAM Role Permissions**
   Ensure the Lambda’s execution role allows:
   - `dynamodb:GetItem`
   - `dynamodb:PutItem`
   - (Optional) CloudWatch Logs: `logs:CreateLogGroup`, `logs:CreateLogStream`, `logs:PutLogEvents`

5. **Enable a Function URL**  
   In the Lambda console, under **“Function URL”**, click **“Create function URL”**:
   - **Auth type:** None (or AWS_IAM for secured access)
   - **CORS**
     - **Allowed origins:** `https://your‑site.com`
     - **Allowed methods:** `GET, OPTIONS`
     - **Allowed headers:** `*` (or restrict to the headers your widget actually uses)
     - **Allow credentials:** Disabled (unless you need cookies/auth)
   - Click **Save**
   - **Copy** the generated URL you’ll reference it in your `site_widget.html`

   > 🔒 By configuring CORS to only allow your own site’s origin, you ensure that no other domains can invoke your Function URL directly, helping to protect your API from unauthorized use.

---

## Front‑End Widget (`site_widget.html`)

Simply include the provided `site_widget.html` file in your web project. It contains all the HTML, CSS, and JavaScript needed to render your HTB stats card.

1. **Place** `site_widget.html` in your site’s public folder.
2. **Edit** the `<script>` section at the bottom:

   ```js
   // Change if you want a different country prefix
   const COUNTRY_PREFIX = "UK";
   const LOCAL_RANK_LABEL = `${COUNTRY_PREFIX} Rank`;

   // Replace with your actual Lambda Function URL
   fetchAndDisplay("https://<your-function-url>");
   ```

3. **Load** the page your card will auto‑fetch and display today’s stats.

---

## Usage

1. **Deploy** your Lambda & DynamoDB as above.
2. **Copy** `site_widget.html` into your web project.
3. **Open** the page in a browser. You should see a centered card with your HTB stats.

---

## Customization

- **Country Prefix**
  Modify the `COUNTRY_PREFIX` constant in `site_widget.html` to match your HTB country code (e.g. `"US"`, `"FR"`).

- **Styling**
  Tweak the CSS in `site_widget.html` to match your site’s design.

---
