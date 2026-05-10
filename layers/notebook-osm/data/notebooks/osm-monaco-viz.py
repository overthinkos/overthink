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
        # Monaco — standalone OSM + GTFS marimo demo

        Self-contained pipeline: this notebook **writes its own Airflow
        DAGs** to `${AIRFLOW_DAGS_DIR}` (one for OSM, one for GTFS),
        **triggers** them in parallel via the Airflow REST API, **polls
        until both succeed**, then runs polars analysis on both
        datasets and renders two folium maps:

        - **Streets map** — martin-served PMTiles produced by the OSM DAG
          (PBF → quackosm → ogr2ogr → tippecanoe → martin).
        - **Transit map** — Monaco bus-stop CircleMarkers on default
          OpenStreetMap raster tiles, produced by the GTFS DAG
          (transitous.org GTFS zip → gtfs-parquet → polars).

        ## URL strategy — server-side vs browser-side

        | Output class | Where it executes | URL handling |
        |---|---|---|
        | **A. Server-side compute** (polars DataFrames — OSM GPU/tag, GTFS analytics) | In the marimo kernel inside the pod | None — kernel has full container-internal reach |
        | **B. Server-rendered HTML, data inlined** (transit folium — bus stops baked into HTML) | Kernel emits self-contained HTML | None — no URL ends up in the output |
        | **C. Lazy client-side fetch** (streets folium — PMTiles via martin) | User's browser on the host | Critical — URL must use **`MARTIN_PUBLIC_URL`** (host-visible) |

        Server-side calls (notebook → Airflow API) use
        `AIRFLOW_API_INTERNAL_URL` (defaults to `http://localhost:8080`,
        works for same-pod airflow; override to e.g. `http://airflow-pod:8080`
        for cross-pod topologies on the shared `ov` network).
        """
    )
    return


@app.cell
def __(Path, os, textwrap):
    # Self-author BOTH pipeline DAGs (OSM + GTFS). Idempotent —
    # overwriting on every notebook run keeps both DAG bodies in sync
    # with this notebook (single source of truth: this cell IS each
    # DAG spec).
    #
    # AIRFLOW_DAGS_DIR is read from env so the notebook works in both
    # same-pod (default) and cross-pod (operator-overridden, shared
    # workspace volume) topologies.
    dags_dir = Path(os.environ.get(
        "AIRFLOW_DAGS_DIR",
        os.path.expanduser("~/workspace/dags"),
    ))
    dags_dir.mkdir(parents=True, exist_ok=True)

    # ---- OSM DAG ----
    osm_dag_id = "notebook_osm_pipeline"
    osm_dag_file = dags_dir / f"{osm_dag_id}.py"
    osm_dag_file.write_text(textwrap.dedent('''
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
                # Smart download: re-fetch only if remote is newer than
                # local. Geofabrik refreshes its extracts daily; checking
                # Last-Modified avoids re-pulling ~12 MB on every run.
                import email.utils
                WORK.mkdir(parents=True, exist_ok=True)
                url = "https://download.geofabrik.de/europe/monaco-latest.osm.pbf"
                out = WORK / "monaco.osm.pbf"
                if out.exists():
                    req = urllib.request.Request(url, method="HEAD")
                    with urllib.request.urlopen(req, timeout=30) as resp:
                        last_mod = resp.headers.get("Last-Modified")
                    if last_mod:
                        remote_ts = email.utils.parsedate_to_datetime(
                            last_mod
                        ).timestamp()
                        if remote_ts <= out.stat().st_mtime:
                            return str(out)
                urllib.request.urlretrieve(url, str(out))
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

    # ---- GTFS DAG ----
    gtfs_dag_id = "notebook_gtfs_pipeline"
    gtfs_dag_file = dags_dir / f"{gtfs_dag_id}.py"
    gtfs_dag_file.write_text(textwrap.dedent('''
        """GTFS transit pipeline self-authored by osm-monaco-viz.py.

        Downloads the Monaco bus-network GTFS feed from transitous.org,
        parses it into Parquet via gtfs-parquet (one .parquet per GTFS
        table — stops, routes, trips, stop_times, etc.). Output lands
        under the workspace volume at ~/workspace/gtfs/.
        """
        import os
        import urllib.request
        from datetime import datetime
        from pathlib import Path

        from airflow.decorators import dag, task

        RAW = Path(os.path.expanduser("~/workspace/gtfs/raw"))
        PARQUET = Path(os.path.expanduser("~/workspace/gtfs/parquet"))


        @dag(
            dag_id="notebook_gtfs_pipeline",
            schedule=None,
            start_date=datetime(2026, 1, 1),
            catchup=False,
            tags=["gtfs", "transit", "notebook"],
        )
        def notebook_gtfs_pipeline():
            @task
            def download_gtfs() -> str:
                # Smart download: HEAD-check Last-Modified before
                # re-fetching. transitous.org refreshes feeds at
                # provider cadence; same pattern as OSM download_pbf.
                import email.utils
                RAW.mkdir(parents=True, exist_ok=True)
                url = "https://api.transitous.org/gtfs/mc_horaires-reseau-urbain-compagnie-des-autobus-de-monaco.gtfs.zip"
                out = RAW / "monaco.gtfs.zip"
                if out.exists():
                    req = urllib.request.Request(url, method="HEAD")
                    with urllib.request.urlopen(req, timeout=30) as resp:
                        last_mod = resp.headers.get("Last-Modified")
                    if last_mod:
                        remote_ts = email.utils.parsedate_to_datetime(
                            last_mod
                        ).timestamp()
                        if remote_ts <= out.stat().st_mtime:
                            return str(out)
                urllib.request.urlretrieve(url, str(out))
                return str(out)

            @task
            def gtfs_to_parquet(zip_path: str) -> str:
                from gtfs_parquet import parse_gtfs, write_parquet
                PARQUET.mkdir(parents=True, exist_ok=True)
                feed = parse_gtfs(zip_path)
                write_parquet(feed, str(PARQUET))
                return str(PARQUET)

            gtfs_to_parquet(download_gtfs())


        notebook_gtfs_pipeline()
    ''').lstrip())

    dag_ids = [osm_dag_id, gtfs_dag_id]
    dag_files = {osm_dag_id: osm_dag_file, gtfs_dag_id: gtfs_dag_file}
    return dag_files, dag_ids, dags_dir


@app.cell
def __(dag_files, dag_ids, os, requests, time):
    # Server-side: container-internal loopback by default. Override
    # AIRFLOW_API_INTERNAL_URL for cross-pod topologies (e.g. set
    # to "http://airflow-pod:8080" when airflow runs in a separate
    # pod on the shared ov podman network).
    _api = os.environ.get("AIRFLOW_API_INTERNAL_URL", "http://localhost:8080")
    _pwd = os.environ["AIRFLOW_ADMIN_PASSWORD"]

    # 1. Get JWT once (used for all DAG operations below).
    _token = requests.post(
        f"{_api}/auth/token",
        json={"username": "admin", "password": _pwd},
        timeout=10,
    ).json()["access_token"]
    _auth = {"Authorization": f"Bearer {_token}"}

    from datetime import datetime, timezone

    # 2. For EACH DAG: wait for scheduler registration → unpause →
    #    trigger → record run_id. Airflow scans the dags folder every
    #    10s (AIRFLOW__DAG_PROCESSOR__REFRESH_INTERVAL=10); 90s gives
    #    headroom for ~9 scan opportunities per DAG.
    dag_run_ids = {}
    for _did in dag_ids:
        _deadline = time.monotonic() + 90
        while time.monotonic() < _deadline:
            _r = requests.get(
                f"{_api}/api/v2/dags/{_did}", headers=_auth, timeout=5,
            )
            if _r.status_code == 200:
                if _r.json().get("is_paused"):
                    requests.patch(
                        f"{_api}/api/v2/dags/{_did}",
                        headers=_auth, json={"is_paused": False}, timeout=5,
                    )
                    time.sleep(1)
                    continue
                break
            time.sleep(2)
        else:
            raise RuntimeError(
                f"Airflow never registered DAG {_did} from {dag_files[_did]}"
            )
        # Trigger. Airflow 3.x requires logical_date in the trigger
        # payload (was auto-generated in 2.x).
        _run = requests.post(
            f"{_api}/api/v2/dags/{_did}/dagRuns",
            headers=_auth,
            json={
                "conf": {},
                "logical_date": datetime.now(timezone.utc).isoformat(),
            },
            timeout=10,
        ).json()
        dag_run_ids[_did] = _run["dag_run_id"]

    # 3. Poll BOTH DAGs concurrently until each reaches success/failed.
    #    10 min cap covers cold-cache downloads (PBF ~12 MB +
    #    GTFS ~770 KB) plus quackosm + tippecanoe + gtfs-parquet.
    dag_run_states = {}
    _deadline = time.monotonic() + 600
    while time.monotonic() < _deadline and len(dag_run_states) < len(dag_ids):
        for _did in dag_ids:
            if _did in dag_run_states:
                continue
            _state = requests.get(
                f"{_api}/api/v2/dags/{_did}/dagRuns/{dag_run_ids[_did]}",
                headers=_auth, timeout=5,
            ).json()["state"]
            if _state in ("success", "failed"):
                dag_run_states[_did] = _state
        if len(dag_run_states) < len(dag_ids):
            time.sleep(3)
    if len(dag_run_states) < len(dag_ids):
        _missing = [d for d in dag_ids if d not in dag_run_states]
        raise TimeoutError(f"DAGs {_missing} did not finish in 10 min")

    # 4. Fail loudly if any DAG ended non-success.
    _failed = [d for d, s in dag_run_states.items() if s != "success"]
    if _failed:
        raise RuntimeError(f"DAG(s) ended non-success: {dag_run_states}")

    # Display the per-DAG state map (marimo renders the dict).
    dag_run_states
    return (dag_run_states,)


@app.cell
def __(dag_run_states, os, pl):
    # Class A — server-side compute, server-rendered table. No URL concern.
    # Gate on the OSM DAG having finished successfully.
    assert dag_run_states["notebook_osm_pipeline"] == "success"
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
    df_gpu  # last-expression render — marimo displays this DataFrame
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
    df_tags  # last-expression render — marimo displays this DataFrame
    return (df_tags,)


@app.cell
def __(folium, os):
    # Class C — lazy client-side fetch from a URL the BROWSER must reach.
    # Tile URL is read from MARTIN_PUBLIC_URL so it carries the host-
    # visible mapping (e.g. http://127.0.0.1:23000), not the container-
    # internal localhost:3000 that wouldn't resolve from the user's host.
    #
    # Standalone — does NOT depend on dag_run_state. The folium WIDGET
    # (tile-URL template) renders immediately on notebook open. Tile
    # fetches start succeeding once the DAG produces monaco.pmtiles for
    # martin to serve; before then, the widget shows an empty Leaflet
    # canvas — same UX as any tile layer whose source goes briefly
    # offline.
    martin = os.environ.get("MARTIN_PUBLIC_URL", "http://127.0.0.1:23000")
    m = folium.Map(location=[43.7384, 7.4246], zoom_start=14, tiles=None)
    folium.TileLayer(
        tiles=f"{martin}/monaco/{{z}}/{{x}}/{{y}}",
        attr="OSM via QuackOSM + tippecanoe + martin",
        name="Monaco OSM",
    ).add_to(m)
    # Set the Figure's (NOT the Map's) height. Branca's _repr_html_
    # only emits the "Make this Notebook Trusted" wrapper when
    # figure.height is None — the wrapper exists for Jupyter classic
    # NB which blocks iframe srcdoc in untrusted notebooks. Setting
    # any explicit height routes through branca's clean iframe path
    # with no trust check, which is what we want in marimo (a non-
    # Jupyter renderer where the wrapper just hides the map).
    m.get_root().height = "500px"
    m


@app.cell
def __(dag_run_states, os, pl):
    # Class A — server-side polars on the GTFS parquet directory
    # produced by notebook_gtfs_pipeline. Reports stop / route counts
    # plus the top routes by stop count (a useful "where does each
    # bus go?" summary for Monaco's compact transit network).
    assert dag_run_states["notebook_gtfs_pipeline"] == "success"
    gtfs_dir = os.path.expanduser("~/workspace/gtfs/parquet")

    df_stops = pl.read_parquet(f"{gtfs_dir}/stops.parquet")
    df_routes = pl.read_parquet(f"{gtfs_dir}/routes.parquet")
    df_trips = pl.read_parquet(f"{gtfs_dir}/trips.parquet")
    df_stop_times = pl.read_parquet(f"{gtfs_dir}/stop_times.parquet")

    # Top routes by distinct-stop count: trips → stop_times → stops.
    df_route_stops = (
        df_trips.lazy()
        .join(df_stop_times.lazy(), on="trip_id")
        .join(df_routes.lazy(), on="route_id")
        .group_by(["route_short_name", "route_long_name"])
        .agg(pl.col("stop_id").n_unique().alias("n_stops"))
        .sort("n_stops", descending=True)
        .head(15)
        .collect()
    )
    gtfs_summary = pl.DataFrame({
        "metric": ["stops", "routes", "trips", "stop_times"],
        "count":  [df_stops.height, df_routes.height,
                   df_trips.height, df_stop_times.height],
    })
    gtfs_summary  # render the summary table
    return df_route_stops, df_stops, gtfs_summary


@app.cell
def __(df_route_stops):
    # Render the top-routes table as the cell's display value.
    df_route_stops
    return


@app.cell
def __(df_stops, folium):
    # Transit map — bus stops as CircleMarkers on default OpenStreetMap
    # raster tiles. Self-contained: the marker coordinates are inlined
    # into the rendered HTML (Class B), no external URL fetches needed
    # except the public OSM raster CDN that folium's default tile layer
    # uses (always reachable from any browser, no host-port concerns).
    #
    # Gating on df_stops (from the GTFS analytics cell) makes this
    # cell run only after the GTFS DAG completes — the marker data
    # is the load-bearing input.
    transit_map = folium.Map(
        location=[43.7384, 7.4246],
        zoom_start=14,
        tiles="OpenStreetMap",
    )
    for _row in df_stops.iter_rows(named=True):
        _lat = _row.get("stop_lat")
        _lon = _row.get("stop_lon")
        _name = _row.get("stop_name") or _row.get("stop_id")
        if _lat is None or _lon is None:
            continue
        folium.CircleMarker(
            location=[float(_lat), float(_lon)],
            radius=4,
            color="#2563eb",
            fill=True,
            fill_color="#3b82f6",
            fill_opacity=0.8,
            popup=str(_name),
        ).add_to(transit_map)
    # Same trust-wrapper bypass as the streets map (set Figure height,
    # not Map height — see the streets folium cell for the full RCA).
    transit_map.get_root().height = "500px"
    transit_map


if __name__ == "__main__":
    app.run()
