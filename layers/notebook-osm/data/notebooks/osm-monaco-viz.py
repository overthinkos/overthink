import marimo

__generated_with = "0.16.0"
app = marimo.App(width="medium")


@app.cell
def __():
    import marimo as mo
    import os
    import time
    import textwrap
    import requests
    from pathlib import Path
    import polars as pl
    import folium
    return Path, folium, mo, os, pl, requests, textwrap, time


@app.cell
def __(mo):
    mo.md(
        r"""
        # Monaco OSM — standalone marimo demo

        Self-contained pipeline: this notebook **writes its own Airflow
        DAG** to `${AIRFLOW_DAGS_DIR}`, **triggers** it via the Airflow
        REST API, **polls** until success, then runs polars analysis
        (GPU + CPU) and a folium map served by martin's PMTiles.

        ## URL strategy — server-side vs browser-side

        | Output class | Where it executes | URL handling |
        |---|---|---|
        | **A. Server-side compute** (polars DataFrames in cells 5–6) | In the marimo kernel inside the pod | None — kernel has full container-internal reach |
        | **B. Server-rendered HTML, data inlined** (none here, but available as fallback) | Kernel emits self-contained HTML | None — no URL ends up in the output |
        | **C. Lazy client-side fetch** (folium PMTiles in cell 7) | User's browser on the host | Critical — URL must use **`MARTIN_PUBLIC_URL`** (host-visible) |

        Server-side calls (notebook → Airflow API in cell 4) use
        `AIRFLOW_API_INTERNAL_URL` (defaults to `http://localhost:8080`,
        works for same-pod airflow; override to e.g. `http://airflow-pod:8080`
        for cross-pod topologies on the shared `ov` network).
        """
    )
    return


@app.cell
def __(Path, os, textwrap):
    # Self-author the OSM pipeline DAG. Idempotent: overwriting the
    # file on every notebook run keeps the DAG body in sync with this
    # notebook (single source of truth — this cell IS the DAG spec).
    #
    # AIRFLOW_DAGS_DIR is read from env so the notebook works in both
    # same-pod (default) and cross-pod (operator-overridden, shared
    # workspace volume) topologies.
    dags_dir = Path(os.environ.get(
        "AIRFLOW_DAGS_DIR",
        os.path.expanduser("~/workspace/dags"),
    ))
    dags_dir.mkdir(parents=True, exist_ok=True)
    dag_id = "notebook_osm_pipeline"
    dag_file = dags_dir / f"{dag_id}.py"
    dag_file.write_text(textwrap.dedent('''
        """OSM pipeline self-authored by osm-monaco-viz.py.

        Downloads Monaco PBF, converts to GeoParquet via quackosm,
        exports GeoJSON via ogr2ogr, builds PMTiles via tippecanoe.
        Output lands under the workspace volume at the paths martin
        already serves.
        """
        import os
        import subprocess
        import urllib.request
        from datetime import datetime
        from pathlib import Path

        from airflow.decorators import dag, task

        WORK = Path(os.path.expanduser("~/workspace/tiles/work"))
        TILES = Path(os.path.expanduser("~/workspace/tiles/pmtiles"))


        @dag(
            dag_id="notebook_osm_pipeline",
            schedule=None,
            start_date=datetime(2026, 1, 1),
            catchup=False,
            tags=["osm", "notebook"],
        )
        def notebook_osm_pipeline():
            @task
            def download_pbf() -> str:
                WORK.mkdir(parents=True, exist_ok=True)
                out = WORK / "monaco.osm.pbf"
                if not out.exists():
                    urllib.request.urlretrieve(
                        "https://download.geofabrik.de/europe/monaco-latest.osm.pbf",
                        str(out),
                    )
                return str(out)

            @task
            def pbf_to_geoparquet(pbf_path: str) -> str:
                import quackosm as qosm
                out = WORK / "monaco.parquet"
                qosm.convert_pbf_to_parquet(pbf_path, result_file_path=str(out))
                return str(out)

            @task
            def geoparquet_to_geojson(parquet_path: str) -> str:
                out = WORK / "monaco.geojson"
                subprocess.run(
                    ["ogr2ogr", "-f", "GeoJSON", str(out), parquet_path],
                    check=True,
                )
                return str(out)

            @task
            def geojson_to_pmtiles(geojson_path: str) -> str:
                TILES.mkdir(parents=True, exist_ok=True)
                out = TILES / "monaco.pmtiles"
                subprocess.run([
                    "tippecanoe", "-o", str(out), "-zg",
                    "--drop-densest-as-needed", "--force", geojson_path,
                ], check=True)
                return str(out)

            geojson_to_pmtiles(
                geoparquet_to_geojson(pbf_to_geoparquet(download_pbf()))
            )


        notebook_osm_pipeline()
    ''').lstrip())
    return dag_file, dag_id, dags_dir


@app.cell
def __(dag_file, dag_id, os, requests, time):
    # Server-side: container-internal loopback by default. Override
    # AIRFLOW_API_INTERNAL_URL for cross-pod topologies (e.g. set
    # to "http://airflow-pod:8080" when airflow runs in a separate
    # pod on the shared ov podman network).
    _api = os.environ.get("AIRFLOW_API_INTERNAL_URL", "http://localhost:8080")
    _pwd = os.environ["AIRFLOW_ADMIN_PASSWORD"]

    # 1. Get JWT.
    _token = requests.post(
        f"{_api}/auth/token",
        json={"username": "admin", "password": _pwd},
        timeout=10,
    ).json()["access_token"]
    _auth = {"Authorization": f"Bearer {_token}"}

    # 2. Wait for the scheduler to register the DAG file we just wrote,
    # and unpause it if it was registered paused. The airflow layer sets
    # AIRFLOW__DAG_PROCESSOR__REFRESH_INTERVAL=10s (vs. upstream default
    # of 300s) so the scan runs ~every 10s; 90s gives headroom for ~9
    # scan opportunities.
    _deadline = time.monotonic() + 90
    while time.monotonic() < _deadline:
        _r = requests.get(f"{_api}/api/v2/dags/{dag_id}", headers=_auth, timeout=5)
        if _r.status_code == 200:
            if _r.json().get("is_paused"):
                requests.patch(
                    f"{_api}/api/v2/dags/{dag_id}",
                    headers=_auth, json={"is_paused": False}, timeout=5,
                )
                time.sleep(1)
                continue
            break
        time.sleep(2)
    else:
        raise RuntimeError(f"Airflow never registered DAG {dag_id} from {dag_file}")

    # 3. Trigger. Airflow 3.x requires logical_date in the trigger
    # payload (was auto-generated in 2.x). Use current UTC.
    from datetime import datetime, timezone
    _run = requests.post(
        f"{_api}/api/v2/dags/{dag_id}/dagRuns",
        headers=_auth,
        json={
            "conf": {},
            "logical_date": datetime.now(timezone.utc).isoformat(),
        },
        timeout=10,
    ).json()
    run_id = _run["dag_run_id"]

    # 4. Poll until success/failed (10 min cap covers cold-cache PBF download).
    _deadline = time.monotonic() + 600
    state = "queued"
    while time.monotonic() < _deadline:
        state = requests.get(
            f"{_api}/api/v2/dags/{dag_id}/dagRuns/{run_id}",
            headers=_auth, timeout=5,
        ).json()["state"]
        if state in ("success", "failed"):
            break
        time.sleep(3)
    else:
        raise TimeoutError(f"DAG {dag_id} run {run_id} did not finish in 10 min")

    if state != "success":
        raise RuntimeError(f"DAG {dag_id} run {run_id} ended in state {state}")
    dag_run_state = state
    return dag_run_state, run_id


@app.cell
def __(dag_run_state, os, pl):
    # Class A — server-side compute, server-rendered table. No URL concern.
    # Gate on dag_run_state so this cell waits for the DAG to finish.
    assert dag_run_state == "success"
    parquet_path = os.path.expanduser("~/workspace/tiles/work/monaco.parquet")

    # cudf-polars-cu13 panics on group_by over Arrow Map<String,String>
    # (the dtype quackosm writes for `tags`). The panic is a Rust unwrap
    # that escapes Polars' fallback machinery. Group on a flat column
    # instead. raise_on_fail=False routes any non-panic unsupported op
    # back to the CPU engine; the polars >=1.30,<1.39 GPUEngine
    # constructor takes only raise_on_fail (no fallback_mode kwarg).
    df_gpu = (
        pl.scan_parquet(parquet_path)
        .with_columns((pl.col("geometry").bin.size() // 1024).alias("geom_kb"))
        .group_by("geom_kb")
        .agg(pl.len().alias("n"))
        .sort("geom_kb")
        .collect(engine=pl.GPUEngine(raise_on_fail=False))
    )
    return df_gpu, parquet_path


@app.cell
def __(parquet_path, pl):
    # Class A — server-side compute via pyarrow. The original goal of
    # this cell was a polars CPU `group_by("tags")` histogram, but
    # polars 1.38 cannot read the quackosm `tags` column at all: its
    # arrow2 MapArray reader panics on `map<string,string>` because it
    # decodes the logical type as List<List<Struct>> and asserts a
    # plain Struct inner. The same panic occurs on .collect() and on
    # .read_parquet(); it is NOT GPU-specific. pyarrow's parquet
    # reader handles the column correctly (`map<string, string>`),
    # so we tally tag KEYS via pyarrow + collections.Counter — more
    # informative than full-tag-set grouping for OSM data.
    import pyarrow.parquet as pq
    from collections import Counter
    tbl = pq.read_table(parquet_path, columns=["tags"])
    _counter = Counter()
    for _row in tbl.column("tags").to_pylist():
        if _row:
            for _k, _v in _row:
                _counter[_k] += 1
    _top = sorted(_counter.items(), key=lambda kv: -kv[1])[:20]
    df_tags = pl.DataFrame(
        {"tag_key": [k for k, _ in _top], "n": [v for _, v in _top]}
    )
    return (df_tags,)


@app.cell
def __(dag_run_state, folium, os):
    # Class C — lazy client-side fetch from a URL the BROWSER must reach.
    # Tile URL is read from MARTIN_PUBLIC_URL so it carries the host-
    # visible mapping (e.g. http://127.0.0.1:23000), not the container-
    # internal localhost:3000 that wouldn't resolve from the user's host.
    assert dag_run_state == "success"
    martin = os.environ.get("MARTIN_PUBLIC_URL", "http://127.0.0.1:23000")
    m = folium.Map(location=[43.7384, 7.4246], zoom_start=14, tiles=None)
    folium.TileLayer(
        tiles=f"{martin}/monaco/{{z}}/{{x}}/{{y}}",
        attr="OSM via QuackOSM + tippecanoe + martin",
        name="Monaco OSM",
    ).add_to(m)
    return (m,)


if __name__ == "__main__":
    app.run()
