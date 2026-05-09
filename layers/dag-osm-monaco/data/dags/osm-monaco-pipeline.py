"""Monaco OSM → PMTiles end-to-end pipeline.

Demonstrates: PBF download → quackosm GeoParquet → ogr2ogr GeoJSON →
tippecanoe PMTiles → martin serve. All output lives under the
user-owned workspace volume — no /opt, no chown, no permission tricks.

Run via:
    airflow dags trigger osm-monaco-pipeline

Output paths (all under ${HOME}/workspace/, the workspace volume):
    work/monaco.osm.pbf       ← downloaded
    work/monaco.parquet       ← quackosm GeoParquet
    work/monaco.geojson       ← ogr2ogr export
    tiles/pmtiles/monaco.pmtiles  ← tippecanoe output, served by martin
"""
import os
from datetime import datetime
from pathlib import Path

from airflow.decorators import dag, task

# Workspace-rooted paths — user-owned, writable by the DAG runner
# (uid 1000), readable by martin (also uid 1000). No /opt, no chown.
WORKSPACE = Path(os.path.expanduser("~/workspace"))
WORK = WORKSPACE / "tiles" / "work"
TILES = WORKSPACE / "tiles" / "pmtiles"


@dag(
    dag_id="osm-monaco-pipeline",
    schedule=None,
    start_date=datetime(2026, 1, 1),
    catchup=False,
    tags=["osm", "demo"],
)
def osm_pipeline():
    @task
    def download_pbf() -> str:
        import urllib.request
        WORK.mkdir(parents=True, exist_ok=True)
        url = "https://download.geofabrik.de/europe/monaco-latest.osm.pbf"
        out = WORK / "monaco.osm.pbf"
        urllib.request.urlretrieve(url, str(out))
        return str(out)

    @task
    def pbf_to_geoparquet(pbf_path: str) -> str:
        import quackosm as qosm
        out = qosm.convert_pbf_to_parquet(
            pbf_path,
            result_file_path=str(WORK / "monaco.parquet"),
        )
        return str(out)

    @task
    def geoparquet_to_geojson(parquet_path: str) -> str:
        import subprocess
        out = WORK / "monaco.geojson"
        subprocess.run(
            ["ogr2ogr", "-f", "GeoJSON", str(out), parquet_path],
            check=True,
        )
        return str(out)

    @task
    def geojson_to_pmtiles(geojson_path: str) -> str:
        import subprocess
        TILES.mkdir(parents=True, exist_ok=True)
        out = TILES / "monaco.pmtiles"
        subprocess.run(
            [
                "tippecanoe",
                "-o", str(out),
                "-zg",
                "--drop-densest-as-needed",
                "--force",
                geojson_path,
            ],
            check=True,
        )
        return str(out)

    geojson_to_pmtiles(geoparquet_to_geojson(pbf_to_geoparquet(download_pbf())))


osm_pipeline()
