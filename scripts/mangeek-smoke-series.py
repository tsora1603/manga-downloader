"""Pick a current ManGeek series for the makefile smoke target."""
import hashlib
import json
import time
from urllib.request import Request, urlopen

nonce = format(time.time_ns() // 1_000_000, "X")
key = hashlib.md5(f"M<{nonce}#MANG33K>D".encode()).hexdigest()
url = f"http://geekstations.com.br/api/v2/pt/home/{nonce}/{key}"
request = Request(url, headers={"Accept": "application/json", "X-Requested-With": "XMLHttpRequest"})
with urlopen(request, timeout=15) as response:
    print(int(json.load(response)["tops"][0]["id"]))
