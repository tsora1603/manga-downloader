"""Regenerate the title-search host list from the Supported sites table."""
import re
from pathlib import Path
from urllib.parse import urlsplit

root = Path(__file__).resolve().parents[1]
section = (root / "README.md").read_text().split("## Supported sites\n", 1)[1].split("\n## ", 1)[0]
rows = re.findall(r"^\| \[([^]]+)\]\((https?://[^)]+)\)(?: \\\*)? \| ([^|]*) \|", section, re.M)
# A missing language note (and a multilingual site) is resolved per result.
foreign = re.compile(r"\b(Arabic|Bulgarian|Burmese|Catalan|Chinese|Czech|Dutch|European Portuguese|French|German|Greek|Hebrew|Hindi|Hungarian|Indonesian|Italian|Japanese|Korean|Malay|Persian|Polish|Romanian|Russian|Spanish|Thai|Turkish|Ukrainian|Vietnamese|pt-PT)\b", re.I)
hosts = {}
for domain, url, note in rows:
    languages = []
    if re.search(r"\bEnglish\b", note, re.I):
        languages.append("en")
    if re.search(r"\bPortuguese\b", note, re.I) and not re.search(r"European Portuguese|pt-PT", note, re.I):
        brazilian = re.search(r"Brazilian Portuguese|pt-BR", note, re.I) or domain.endswith(".br")
        languages.append("pt-br" if brazilian else "pt")
    if "multilingual" in note.lower() or (not languages and not foreign.search(note)):
        languages = ["mixed"]
    for host in {domain, urlsplit(url).hostname}:
        hosts[host.lower().removeprefix("www.")] = ",".join(languages) or "other"

out = root / "grabber/search-sites.tsv"
out.write_text("".join(f"{host}\t{hosts[host]}\n" for host in sorted(hosts)))
