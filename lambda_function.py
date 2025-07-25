import json
import logging
import requests
import os
import boto3
from datetime import date

# —————————————————————————————————————————————————————————————————————
# Configure root logger to emit INFO‑level messages
# —————————————————————————————————————————————————————————————————————
logger = logging.getLogger()
logger.setLevel(logging.INFO)


def lambda_handler(event, context):
    """
    AWS Lambda entry point.
    1. Check DynamoDB for today’s cached HTB data.
    2. If present, strip out the 'date' key and return the rest.
    3. On cache miss, fetch fresh data from the HTB API, cache it, and return it.
    """
    # ISO‑formatted string for today (used as the DynamoDB partition key)
    today_str = date.today().isoformat()

    # DynamoDB table name must be supplied via env‑var TABLE_NAME
    table_name = os.environ.get("TABLE_NAME")
    if not table_name:
        logger.error("Missing TABLE_NAME environment variable")
        return {"error": "TABLE_NAME not configured"}

    # Initialize DynamoDB resource and select the target table
    dynamodb = boto3.resource("dynamodb")
    table = dynamodb.Table(table_name)

    # 1) Attempt to fetch today’s record from DynamoDB
    try:
        resp = table.get_item(Key={"date": today_str})
    except Exception as e:
        logger.error(f"Error fetching item from DynamoDB: {e}")
        return {"error": "Database lookup failed"}

    # 2) Cache hit: remove the 'date' key and return the rest of the data
    if "Item" in resp:
        item = resp["Item"]
        item.pop("date", None)
        logger.info(f"Cache hit: returning data from DynamoDB for date {today_str}")
        return item

    # 3) Cache miss → fetch fresh data from Hack The Box API
    item = {"date": today_str}
    logger.info(f"No DynamoDB entry for {today_str}, fetching from HTB API")
    data = get_rankings_from_htb()
    if data is None:
        logger.error("Failed to fetch rankings from HTB")
        table.put_item(Item=item)
        return {"error": "Could not retrieve rankings"}

    # 4) Build the new item (including today's date) and write it back
    item.update(data)
    try:
        table.put_item(Item=item)
        logger.info(f"Stored fresh HTB data in DynamoDB under key {today_str}")
    except Exception as e:
        logger.error(f"Error writing item to DynamoDB: {e}")
        # Even if caching fails, return the fetched data

    logger.info("Returning fresh data from HTB API")
    return data


def get_rankings_from_htb():
    """
    Fetches HTB user profile and local-country rankings.
    - Reads USER_ID and TOKEN from environment variables.
    - Retrieves basic profile (owns, bloods, rank, global rank).
    - Determines the user's country code, then fetches that country’s rankings.
    Returns a dict of metrics or None on any failure.
    """
    # Sensitive values supplied via env‑vars
    USER_ID = os.environ.get("USER_ID", None)
    APP_TOKEN = os.environ.get("TOKEN", None)

    # Base URLs for HTB API calls
    LOCAL_RANKINGS_URL = "https://labs.hackthebox.com/api/v4/rankings/country/"
    USER_URL = "https://labs.hackthebox.com/api/v4/user/profile/basic/"

    # Must have both TOKEN and USER_ID to proceed
    if APP_TOKEN is None or USER_ID is None:
        return None

    # Standard headers for both HTTP requests
    HEADERS = {
        "Authorization": f"Bearer {APP_TOKEN}",
        "User-Agent": (
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
            "AppleWebKit/537.36 (KHTML, like Gecko) "
            "Chrome/138.0.0.0 Safari/537.36"
        ),
    }

    USERNAME = None
    COUNTRYCODE = None
    User_Info = {}

    # 1) Fetch basic user profile
    USER_DATA = requests.get(f"{USER_URL}{USER_ID}", headers=HEADERS)
    logger.info(f"USERDATA status code: {USER_DATA.status_code}")
    if USER_DATA.status_code == 200:
        profile = USER_DATA.json().get("profile", {})
        logger.info(f"USERDATA profile: {profile}")

        USERNAME = profile.get("name")
        COUNTRYCODE = profile.get("country_code")
        User_Info["System_Owns"] = profile.get("system_owns")
        User_Info["User_Owns"] = profile.get("user_owns")
        User_Info["System_Bloods"] = profile.get("system_bloods")
        User_Info["User_Bloods"] = profile.get("user_bloods")
        User_Info["Rank"] = profile.get("rank")
        User_Info["User_Global_Rank"] = profile.get("ranking")

    # Abort if we didn’t get the username or country code
    if USERNAME is None or COUNTRYCODE is None:
        return None

    # 2) Fetch local‑country rankings and assign this user’s rank
    LOCAL_RANKINGS = requests.get(
        f"{LOCAL_RANKINGS_URL}{COUNTRYCODE}/members", headers=HEADERS
    )
    if LOCAL_RANKINGS.status_code == 200:
        for ranking in LOCAL_RANKINGS.json().get("data", {}).get("rankings", []):
            if ranking.get("name") == USERNAME:
                User_Info["Local_Rank"] = ranking.get("rank")
                break

    return User_Info
