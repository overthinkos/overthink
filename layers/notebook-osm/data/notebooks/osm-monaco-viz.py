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
        DAGs** to `${AIRFLOW_DAGS_DIR}` (five DAGs — OSM, GTFS, and three
        parallel vector-tile generators), **triggers** them via the
        Airflow REST API, **polls until each succeeds**, then runs
        polars analysis + a cudf-polars GPU/CPU benchmark on the OSM
        GeoParquet and renders five maps:

        - **Streets map** (baseline) — PMTiles produced by the OSM DAG
          (PBF → quackosm → duckdb-spatial → tippecanoe → martin).
        - **gpq-tiles map** — direct GeoParquet → PMTiles via the
          geoparquet-io/gpq-tiles Rust converter (no GeoJSON intermediate).
        - **DuckDB ST_AsMVT map** — per-tile MVT encoding in SQL, archive
          assembled via the `pmtiles` Python package's Writer.
        - **DuckDB → freestiler map** — same DuckDB SQL front-end as
          ST_AsMVT, but the per-tile encoding + PMTiles packing happens
          inside freestiler's in-process Rust engine in one library call.
        - **Transit map** — Monaco bus-stop CircleMarkers on default
          OpenStreetMap raster tiles, produced by the GTFS DAG
          (transitous.org GTFS zip → gtfs-parquet → polars).

        The four vector-tile maps share martin's tile-server contract
        (each PMTiles lands as a sibling source under
        `${MARTIN_PUBLIC_URL}/<source-name>/{z}/{x}/{y}`), so the
        visual differences = engine differences, not styling tricks.
        Use the PMTiles Viewer at `http://127.0.0.1:28001/` to inspect
        each archive's bbox / zoom range / metadata.

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
        os.path.expanduser("/workspace/dags"),
    ))
    dags_dir.mkdir(parents=True, exist_ok=True)

    # ---- OSM DAG ----
    osm_dag_id = "notebook_osm_pipeline"
    osm_dag_file = dags_dir / f"{osm_dag_id}.py"
    osm_dag_file.write_text(textwrap.dedent('''
        """OSM pipeline self-authored by osm-monaco-viz.py.

        Downloads Monaco PBF, converts to GeoParquet via quackosm,
        exports GeoJSON via duckdb-spatial (ST_AsGeoJSON), builds PMTiles via tippecanoe.
        Output lands under the workspace volume at the paths martin
        already serves.
        """
        import os
        import subprocess
        import urllib.request
        from datetime import datetime
        from pathlib import Path

        from airflow.decorators import dag, task

        WORK = Path(os.path.expanduser("/workspace/tiles/work"))
        TILES = Path(os.path.expanduser("/workspace/tiles/pmtiles"))


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
                # The HEAD probe is an OPTIMIZATION — its failure (502
                # / 503 from geofabrik's CDN, intermittent) is NOT a
                # task-killing error because the actual GET below is the
                # source of truth. Catch HEAD errors narrowly and fall
                # through; let any GET error propagate.
                import email.utils
                import shutil
                WORK.mkdir(parents=True, exist_ok=True)
                url = "https://download.geofabrik.de/europe/monaco-latest.osm.pbf"
                out = WORK / "monaco.osm.pbf"
                if out.exists():
                    try:
                        req = urllib.request.Request(url, method="HEAD")
                        with urllib.request.urlopen(req, timeout=30) as resp:
                            last_mod = resp.headers.get("Last-Modified")
                        if last_mod:
                            remote_ts = email.utils.parsedate_to_datetime(
                                last_mod
                            ).timestamp()
                            if remote_ts <= out.stat().st_mtime:
                                return str(out)
                    except urllib.error.HTTPError:
                        # HEAD endpoint flaky; let the GET below decide.
                        pass
                # urllib.request.urlretrieve has no timeout parameter
                # and uses the (very long, sometimes infinite) socket
                # default. Use urlopen with an explicit 300s read
                # timeout so a slow geofabrik.de surfaces a real error
                # rather than blocking forever. Write to a `.part`
                # tempfile first and atomically rename on success so a
                # partial download never corrupts the cache.
                tmp = out.with_suffix(".pbf.part")
                with urllib.request.urlopen(url, timeout=300) as resp:
                    with open(tmp, "wb") as f:
                        shutil.copyfileobj(resp, f)
                tmp.replace(out)

            @task
            def pbf_to_geoparquet(pbf_path: str) -> str:
                import quackosm as qosm
                out = WORK / "monaco.parquet"
                qosm.convert_pbf_to_parquet(pbf_path, result_file_path=str(out))
                return str(out)

            @task
            def geoparquet_to_geojson(parquet_path: str) -> str:
                # CachyOS rolling-release drift broke ogr2ogr's parquet
                # driver — libarrow_dataset.so soname (2300 vs current)
                # makes the GDAL Parquet plugin fail to load, taking the
                # entire ogr2ogr datasource-open path with it. DuckDB
                # ships its own arrow + a parquet reader built in, plus
                # the spatial extension provides ST_AsGeoJSON for the
                # geometry → GeoJSON-string conversion.
                #
                # Subtle: DuckDB's spatial extension parses GeoParquet
                # metadata when `INSTALL spatial; LOAD spatial;` is
                # active and exposes the geometry column as
                # GEOMETRY('OGC:CRS84') directly. So we pass the column
                # straight to ST_AsGeoJSON — no ST_GeomFromWKB lift
                # needed (and adding one would error with
                # "No function matches st_geomfromwkb(GEOMETRY(...))").
                import duckdb
                import json as _json
                out = WORK / "monaco.geojson"
                con = duckdb.connect()
                con.execute("INSTALL spatial; LOAD spatial;")
                rows = con.execute(f"""
                    SELECT ST_AsGeoJSON(geometry) AS geom_json
                    FROM read_parquet('{parquet_path}')
                """).fetchall()
                features = [
                    {"type": "Feature",
                     "properties": {},
                     "geometry": _json.loads(geom)}
                    for (geom,) in rows
                    if geom
                ]
                with open(out, "w") as f:
                    _json.dump(
                        {"type": "FeatureCollection", "features": features},
                        f,
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

            @task
            def reload_martin(pmtiles_path: str) -> str:
                # Martin caches the pmtiles file mtime when its server
                # starts, then refuses tile requests with "Underlying
                # data source was modified" once the file changes.
                # Restarting martin via supervisord makes it re-scan
                # the directory and pick up the fresh pmtiles. uid 1000
                # has supervisorctl access in this image.
                # Four DAGs each end with `reload_martin` and run in
                # parallel. Two synchronization primitives are at play:
                #   1. flock — serializes the supervisorctl invocations
                #      so only one restart runs at a time globally.
                #   2. TCP readiness probe + /catalog membership check —
                #      verifies the END STATE (martin RUNNING +
                #      our source listed) instead of trusting
                #      supervisorctl's exit code, which can be non-zero
                #      even when martin ends up healthy (supervisord's
                #      internal spawn-window races back-to-back
                #      restarts).
                import fcntl
                import socket
                import time as _time
                import urllib.request
                import json as _json
                source_name = pmtiles_path.rsplit("/", 1)[-1].rsplit(".", 1)[0]
                with open("/tmp/ov-martin-restart.lock", "w") as _lock:
                    fcntl.flock(_lock.fileno(), fcntl.LOCK_EX)
                    subprocess.run(
                        ["supervisorctl", "restart", "martin"],
                        check=False,
                    )
                # Readiness probe — bounded wait for the TCP port to
                # accept connections after the restart. This is the
                # canonical synchronization primitive for "wait until
                # external service X is ready" (R4 explicitly permits
                # readiness probes; what R4 forbids is sleep-as-retry).
                _deadline = _time.monotonic() + 30
                while _time.monotonic() < _deadline:
                    try:
                        with socket.create_connection(
                            ("localhost", 3000), timeout=2,
                        ):
                            break
                    except (ConnectionRefusedError, OSError, socket.timeout):
                        _time.sleep(0.5)
                else:
                    raise RuntimeError(
                        "martin port 3000 not reachable 30s after restart",
                    )
                # End-state assertion — martin's /catalog must list the
                # source we just wrote. This is the actual success
                # criterion, replacing the unreliable exit-code check.
                with urllib.request.urlopen(
                    "http://localhost:3000/catalog", timeout=10,
                ) as _resp:
                    _catalog = _json.load(_resp)
                if source_name not in _catalog.get("tiles", {}):
                    raise RuntimeError(
                        f"martin /catalog missing source '{source_name}' "
                        f"after reload; available="
                        f"{sorted(_catalog.get('tiles', {}).keys())}",
                    )
                return pmtiles_path

            reload_martin(geojson_to_pmtiles(
                geoparquet_to_geojson(pbf_to_geoparquet(download_pbf()))
            ))


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
        under the workspace volume at /workspace/gtfs/.
        """
        import os
        import urllib.request
        from datetime import datetime
        from pathlib import Path

        from airflow.decorators import dag, task

        RAW = Path(os.path.expanduser("/workspace/gtfs/raw"))
        PARQUET = Path(os.path.expanduser("/workspace/gtfs/parquet"))


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

    # ---- Pipeline 2: gpq-tiles DAG ----
    # Direct GeoParquet → PMTiles via geoparquet-io/gpq-tiles (Rust;
    # streaming, memory-bounded; production-grade for large datasets).
    # Reads the SAME monaco.parquet the original OSM DAG produced, so
    # this DAG only runs after notebook_osm_pipeline succeeds.
    gpqtiles_dag_id = "notebook_osm_gpqtiles_pipeline"
    gpqtiles_dag_file = dags_dir / f"{gpqtiles_dag_id}.py"
    gpqtiles_dag_file.write_text(textwrap.dedent('''
        """gpq-tiles alternative vector-tile pipeline.

        Skips tippecanoe entirely: gpq-tiles reads the GeoParquet
        directly and emits a PMTiles archive in one pass. Output is
        a sibling source under martin's watched directory, so the
        existing martin tile server auto-discovers it.
        """
        import os
        import subprocess
        from datetime import datetime
        from pathlib import Path

        from airflow.decorators import dag, task

        WORK = Path(os.path.expanduser("/workspace/tiles/work"))
        TILES = Path(os.path.expanduser("/workspace/tiles/pmtiles"))


        @dag(
            dag_id="notebook_osm_gpqtiles_pipeline",
            schedule=None,
            start_date=datetime(2026, 1, 1),
            catchup=False,
            tags=["osm", "notebook", "gpq-tiles"],
        )
        def notebook_osm_gpqtiles_pipeline():
            @task
            def gpqtiles_convert() -> str:
                parquet_path = WORK / "monaco.parquet"
                TILES.mkdir(parents=True, exist_ok=True)
                out = TILES / "monaco-gpqtiles.pmtiles"
                # gpq-tiles is a system binary in this image (the
                # osm-tools layer cargo-installs it because PyPI wheels
                # don't cover our Python 3.13 / linux x86_64 combo and
                # the pixi env's no-build = true blocks sdist resolution).
                subprocess.run([
                    "/usr/local/bin/gpq-tiles",
                    str(parquet_path), str(out),
                    "--min-zoom", "0",
                    "--max-zoom", "14",
                    "--drop-densest-as-needed",
                ], check=True)
                return str(out)

            @task
            def reload_martin(pmtiles_path: str) -> str:
                # Four DAGs each end with `reload_martin` and run in
                # parallel. Two synchronization primitives are at play:
                #   1. flock — serializes the supervisorctl invocations
                #      so only one restart runs at a time globally.
                #   2. TCP readiness probe + /catalog membership check —
                #      verifies the END STATE (martin RUNNING +
                #      our source listed) instead of trusting
                #      supervisorctl's exit code, which can be non-zero
                #      even when martin ends up healthy (supervisord's
                #      internal spawn-window races back-to-back
                #      restarts).
                import fcntl
                import socket
                import time as _time
                import urllib.request
                import json as _json
                source_name = pmtiles_path.rsplit("/", 1)[-1].rsplit(".", 1)[0]
                with open("/tmp/ov-martin-restart.lock", "w") as _lock:
                    fcntl.flock(_lock.fileno(), fcntl.LOCK_EX)
                    subprocess.run(
                        ["supervisorctl", "restart", "martin"],
                        check=False,
                    )
                # Readiness probe — bounded wait for the TCP port to
                # accept connections after the restart. This is the
                # canonical synchronization primitive for "wait until
                # external service X is ready" (R4 explicitly permits
                # readiness probes; what R4 forbids is sleep-as-retry).
                _deadline = _time.monotonic() + 30
                while _time.monotonic() < _deadline:
                    try:
                        with socket.create_connection(
                            ("localhost", 3000), timeout=2,
                        ):
                            break
                    except (ConnectionRefusedError, OSError, socket.timeout):
                        _time.sleep(0.5)
                else:
                    raise RuntimeError(
                        "martin port 3000 not reachable 30s after restart",
                    )
                # End-state assertion — martin's /catalog must list the
                # source we just wrote. This is the actual success
                # criterion, replacing the unreliable exit-code check.
                with urllib.request.urlopen(
                    "http://localhost:3000/catalog", timeout=10,
                ) as _resp:
                    _catalog = _json.load(_resp)
                if source_name not in _catalog.get("tiles", {}):
                    raise RuntimeError(
                        f"martin /catalog missing source '{source_name}' "
                        f"after reload; available="
                        f"{sorted(_catalog.get('tiles', {}).keys())}",
                    )
                return pmtiles_path

            reload_martin(gpqtiles_convert())


        notebook_osm_gpqtiles_pipeline()
    ''').lstrip())

    # ---- Pipeline 3: DuckDB ST_AsMVT + pmtiles.Writer DAG ----
    # The "by hand" reference path. DuckDB's spatial extension encodes
    # one MVT-PBF blob per (z, x, y) tile via the ST_AsMVT aggregate;
    # the `pmtiles` Python package's Writer packs the blob stream into
    # a single PMTiles archive. No third-party tile generator involved.
    duckdb_mvt_dag_id = "notebook_osm_duckdb_mvt_pipeline"
    duckdb_mvt_dag_file = dags_dir / f"{duckdb_mvt_dag_id}.py"
    duckdb_mvt_dag_file.write_text(textwrap.dedent('''
        """DuckDB ST_AsMVT + pmtiles.Writer pipeline.

        Per-tile MVT generation in SQL; PMTiles archive assembly in
        Python. Scoped to z=10..14 over Monaco's bbox to keep tile
        count tractable (~340 tiles).
        """
        import os
        import math
        import subprocess
        from datetime import datetime
        from pathlib import Path

        from airflow.decorators import dag, task

        WORK = Path(os.path.expanduser("/workspace/tiles/work"))
        TILES = Path(os.path.expanduser("/workspace/tiles/pmtiles"))


        def _tile_coords_for_bbox(min_lon, min_lat, max_lon, max_lat, min_z, max_z):
            """Yield (z, x, y) tile coords covering the bbox at each zoom."""
            for z in range(min_z, max_z + 1):
                n = 2 ** z
                def lon_to_x(lon):
                    return int((lon + 180.0) / 360.0 * n)
                def lat_to_y(lat):
                    rad = math.radians(lat)
                    return int((1.0 - math.log(math.tan(rad) + 1 / math.cos(rad)) / math.pi) / 2.0 * n)
                x_lo, x_hi = lon_to_x(min_lon), lon_to_x(max_lon)
                # tile Y goes top-down (lat_to_y inverts), so max_lat is the low Y.
                y_lo, y_hi = lat_to_y(max_lat), lat_to_y(min_lat)
                for x in range(min(x_lo, x_hi), max(x_lo, x_hi) + 1):
                    for y in range(min(y_lo, y_hi), max(y_lo, y_hi) + 1):
                        yield z, x, y


        @dag(
            dag_id="notebook_osm_duckdb_mvt_pipeline",
            schedule=None,
            start_date=datetime(2026, 1, 1),
            catchup=False,
            tags=["osm", "notebook", "duckdb-mvt"],
        )
        def notebook_osm_duckdb_mvt_pipeline():
            @task
            def encode_to_pmtiles() -> str:
                import duckdb
                from pmtiles.writer import Writer
                from pmtiles.tile import TileType, Compression, zxy_to_tileid

                parquet_path = WORK / "monaco.parquet"
                TILES.mkdir(parents=True, exist_ok=True)
                out = TILES / "monaco-duckdb-mvt.pmtiles"

                # Monaco bbox (approx): lon 7.40-7.45, lat 43.71-43.77
                MIN_LON, MAX_LON = 7.40, 7.45
                MIN_LAT, MAX_LAT = 43.71, 43.77
                MIN_Z, MAX_Z = 10, 14

                con = duckdb.connect()
                con.execute("INSTALL spatial; LOAD spatial;")

                # Build the source table once (in-memory view over the
                # parquet) so each per-tile query can run the same
                # ST_AsMVT_Geom-then-aggregate without re-reading from
                # disk N times.
                con.execute(f"""
                    CREATE TEMP TABLE src AS
                    SELECT geometry, ST_AsText(geometry) AS wkt
                    FROM read_parquet('{parquet_path}')
                    WHERE geometry IS NOT NULL
                """)

                tiles_written = 0
                with open(out, "wb") as f:
                    writer = Writer(f)
                    for z, x, y in _tile_coords_for_bbox(
                        MIN_LON, MIN_LAT, MAX_LON, MAX_LAT, MIN_Z, MAX_Z,
                    ):
                        # ST_AsMVTGeom transforms geometry from lon/lat
                        # to tile-local coords; ST_AsMVT aggregates the
                        # resulting rows into a single MVT-PBF blob.
                        # DuckDB Spatial uses the PostGIS naming
                        # convention: ST_AsMVTGeom is one word.
                        #
                        # Subtle: ST_AsMVTGeom wants the `bounds`
                        # argument as a BOX_2D, not a GEOMETRY. The
                        # ST_TileEnvelope built-in returns GEOMETRY,
                        # so wrap it in ST_Extent to project to a
                        # BOX_2D. ST_Intersects in the WHERE clause is
                        # happy with the GEOMETRY form, so we keep
                        # that one un-wrapped.
                        row = con.execute(f"""
                            SELECT ST_AsMVT({{geom: ST_AsMVTGeom(geometry, ST_Extent(ST_TileEnvelope({z}, {x}, {y})))}}) AS tile
                            FROM src
                            WHERE ST_Intersects(geometry, ST_TileEnvelope({z}, {x}, {y}))
                        """).fetchone()
                        if row and row[0]:
                            # pmtiles.Writer.write_tile takes a single
                            # encoded tile-id (Hilbert curve over z/x/y),
                            # not the three coords separately.
                            writer.write_tile(zxy_to_tileid(z, x, y), row[0])
                            tiles_written += 1
                    writer.finalize(
                        {
                            "tile_type": TileType.MVT,
                            "tile_compression": Compression.NONE,
                            "min_zoom": MIN_Z,
                            "max_zoom": MAX_Z,
                            "min_lon_e7": int(MIN_LON * 1e7),
                            "min_lat_e7": int(MIN_LAT * 1e7),
                            "max_lon_e7": int(MAX_LON * 1e7),
                            "max_lat_e7": int(MAX_LAT * 1e7),
                            "center_zoom": MAX_Z,
                            "center_lon_e7": int((MIN_LON + MAX_LON) / 2 * 1e7),
                            "center_lat_e7": int((MIN_LAT + MAX_LAT) / 2 * 1e7),
                        },
                        {"vector_layers": [{"id": "monaco", "fields": {}}]},
                    )
                print(f"wrote {tiles_written} tiles to {out}")
                return str(out)

            @task
            def reload_martin(pmtiles_path: str) -> str:
                # Four DAGs each end with `reload_martin` and run in
                # parallel. Two synchronization primitives are at play:
                #   1. flock — serializes the supervisorctl invocations
                #      so only one restart runs at a time globally.
                #   2. TCP readiness probe + /catalog membership check —
                #      verifies the END STATE (martin RUNNING +
                #      our source listed) instead of trusting
                #      supervisorctl's exit code, which can be non-zero
                #      even when martin ends up healthy (supervisord's
                #      internal spawn-window races back-to-back
                #      restarts).
                import fcntl
                import socket
                import time as _time
                import urllib.request
                import json as _json
                source_name = pmtiles_path.rsplit("/", 1)[-1].rsplit(".", 1)[0]
                with open("/tmp/ov-martin-restart.lock", "w") as _lock:
                    fcntl.flock(_lock.fileno(), fcntl.LOCK_EX)
                    subprocess.run(
                        ["supervisorctl", "restart", "martin"],
                        check=False,
                    )
                # Readiness probe — bounded wait for the TCP port to
                # accept connections after the restart. This is the
                # canonical synchronization primitive for "wait until
                # external service X is ready" (R4 explicitly permits
                # readiness probes; what R4 forbids is sleep-as-retry).
                _deadline = _time.monotonic() + 30
                while _time.monotonic() < _deadline:
                    try:
                        with socket.create_connection(
                            ("localhost", 3000), timeout=2,
                        ):
                            break
                    except (ConnectionRefusedError, OSError, socket.timeout):
                        _time.sleep(0.5)
                else:
                    raise RuntimeError(
                        "martin port 3000 not reachable 30s after restart",
                    )
                # End-state assertion — martin's /catalog must list the
                # source we just wrote. This is the actual success
                # criterion, replacing the unreliable exit-code check.
                with urllib.request.urlopen(
                    "http://localhost:3000/catalog", timeout=10,
                ) as _resp:
                    _catalog = _json.load(_resp)
                if source_name not in _catalog.get("tiles", {}):
                    raise RuntimeError(
                        f"martin /catalog missing source '{source_name}' "
                        f"after reload; available="
                        f"{sorted(_catalog.get('tiles', {}).keys())}",
                    )
                return pmtiles_path

            reload_martin(encode_to_pmtiles())


        notebook_osm_duckdb_mvt_pipeline()
    ''').lstrip())

    # ---- Pipeline 4: DuckDB → freestiler DAG ----
    # The "by library" companion to the ST_AsMVT pipeline. Same DuckDB
    # SQL front-end, but the per-tile encoding loop + PMTiles packing
    # is delegated to freestiler's in-process Rust engine via
    # freestile_query() — one library call replaces the Python loop.
    duckdb_freestiler_dag_id = "notebook_osm_duckdb_freestiler_pipeline"
    duckdb_freestiler_dag_file = dags_dir / f"{duckdb_freestiler_dag_id}.py"
    duckdb_freestiler_dag_file.write_text(textwrap.dedent('''
        """DuckDB → freestiler pipeline.

        Same DuckDB front-end as the AsMVT pipeline; freestiler's
        Rust tiling engine handles the per-tile MVT encoding +
        PMTiles archive packing in one library call.
        """
        import os
        import subprocess
        from datetime import datetime
        from pathlib import Path

        from airflow.decorators import dag, task

        WORK = Path(os.path.expanduser("/workspace/tiles/work"))
        TILES = Path(os.path.expanduser("/workspace/tiles/pmtiles"))


        @dag(
            dag_id="notebook_osm_duckdb_freestiler_pipeline",
            schedule=None,
            start_date=datetime(2026, 1, 1),
            catchup=False,
            tags=["osm", "notebook", "duckdb-freestiler"],
        )
        def notebook_osm_duckdb_freestiler_pipeline():
            @task
            def freestiler_convert() -> str:
                import freestiler

                parquet_path = WORK / "monaco.parquet"
                TILES.mkdir(parents=True, exist_ok=True)
                out = TILES / "monaco-duckdb-freestiler.pmtiles"

                # freestiler accepts either a file path (sf/spatial-file
                # input) OR a DuckDB SQL query. Use the SQL form to
                # demonstrate the DuckDB-front-end pathway. The API
                # surface (function name + kwargs) is verified at
                # runtime via getattr — gracefully reports the actual
                # surface if it differs from the expected API.
                query = f"SELECT * FROM read_parquet('{parquet_path}')"
                if hasattr(freestiler, "freestile_query"):
                    freestiler.freestile_query(
                        query=query,
                        output=str(out),
                        layer_name="monaco",
                        min_zoom=0,
                        max_zoom=14,
                    )
                elif hasattr(freestiler, "freestile"):
                    freestiler.freestile(
                        input=query,
                        output=str(out),
                        layer_name="monaco",
                        min_zoom=0,
                        max_zoom=14,
                    )
                else:
                    public = sorted(n for n in dir(freestiler) if not n.startswith("_"))
                    raise RuntimeError(
                        f"freestiler public API: {public} — expected "
                        "freestile_query or freestile; adapt this task."
                    )
                return str(out)

            @task
            def reload_martin(pmtiles_path: str) -> str:
                # Four DAGs each end with `reload_martin` and run in
                # parallel. Two synchronization primitives are at play:
                #   1. flock — serializes the supervisorctl invocations
                #      so only one restart runs at a time globally.
                #   2. TCP readiness probe + /catalog membership check —
                #      verifies the END STATE (martin RUNNING +
                #      our source listed) instead of trusting
                #      supervisorctl's exit code, which can be non-zero
                #      even when martin ends up healthy (supervisord's
                #      internal spawn-window races back-to-back
                #      restarts).
                import fcntl
                import socket
                import time as _time
                import urllib.request
                import json as _json
                source_name = pmtiles_path.rsplit("/", 1)[-1].rsplit(".", 1)[0]
                with open("/tmp/ov-martin-restart.lock", "w") as _lock:
                    fcntl.flock(_lock.fileno(), fcntl.LOCK_EX)
                    subprocess.run(
                        ["supervisorctl", "restart", "martin"],
                        check=False,
                    )
                # Readiness probe — bounded wait for the TCP port to
                # accept connections after the restart. This is the
                # canonical synchronization primitive for "wait until
                # external service X is ready" (R4 explicitly permits
                # readiness probes; what R4 forbids is sleep-as-retry).
                _deadline = _time.monotonic() + 30
                while _time.monotonic() < _deadline:
                    try:
                        with socket.create_connection(
                            ("localhost", 3000), timeout=2,
                        ):
                            break
                    except (ConnectionRefusedError, OSError, socket.timeout):
                        _time.sleep(0.5)
                else:
                    raise RuntimeError(
                        "martin port 3000 not reachable 30s after restart",
                    )
                # End-state assertion — martin's /catalog must list the
                # source we just wrote. This is the actual success
                # criterion, replacing the unreliable exit-code check.
                with urllib.request.urlopen(
                    "http://localhost:3000/catalog", timeout=10,
                ) as _resp:
                    _catalog = _json.load(_resp)
                if source_name not in _catalog.get("tiles", {}):
                    raise RuntimeError(
                        f"martin /catalog missing source '{source_name}' "
                        f"after reload; available="
                        f"{sorted(_catalog.get('tiles', {}).keys())}",
                    )
                return pmtiles_path

            reload_martin(freestiler_convert())


        notebook_osm_duckdb_freestiler_pipeline()
    ''').lstrip())

    dag_ids = [
        osm_dag_id, gtfs_dag_id,
        gpqtiles_dag_id, duckdb_mvt_dag_id, duckdb_freestiler_dag_id,
    ]
    dag_files = {
        osm_dag_id: osm_dag_file,
        gtfs_dag_id: gtfs_dag_file,
        gpqtiles_dag_id: gpqtiles_dag_file,
        duckdb_mvt_dag_id: duckdb_mvt_dag_file,
        duckdb_freestiler_dag_id: duckdb_freestiler_dag_file,
    }
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
    parquet_path = os.path.expanduser("/workspace/tiles/work/monaco.parquet")

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
def __(dag_run_states, parquet_path, pl):
    # Class A — server-side DuckDB Spatial query over the same OSM
    # parquet. DuckDB's `spatial` extension reads GeoParquet's
    # geometry-column metadata and exposes the column as
    # GEOMETRY('OGC:CRS84') directly — so ST_GeometryType can classify
    # rows without an intermediate ST_GeomFromWKB lift (which would
    # error with "No function matches st_geometrytype(GEOMETRY(...))"
    # because the geometry is no longer a raw BLOB at this point).
    # This cell answers the basic sanity question: per-geometry-type
    # counts.
    assert dag_run_states["notebook_osm_pipeline"] == "success"
    import duckdb
    _con = duckdb.connect()
    _con.execute("INSTALL spatial; LOAD spatial;")
    df_duckdb_spatial = _con.execute(f"""
        SELECT
            ST_GeometryType(geometry) AS geom_type,
            COUNT(*) AS n
        FROM read_parquet('{parquet_path}')
        GROUP BY 1
        ORDER BY n DESC
    """).pl()
    df_duckdb_spatial  # marimo renders the Polars DataFrame
    return (df_duckdb_spatial,)


@app.cell
def __(dag_run_states, parquet_path, pl):
    # Class A — polars-st adds GEOS-backed spatial operations as a
    # Polars expression namespace (`.st.*`). This cell decodes the
    # WKB geometry column, computes per-feature bounding-box area,
    # and reports the 10 largest polygons. Pure CPU — cudf-polars-cu13
    # falls back to the CPU executor for the .st.* namespace, which is
    # exactly what we want here (the spatial ops aren't GPU-accelerated).
    assert dag_run_states["notebook_osm_pipeline"] == "success"
    import polars_st as st
    df_polars_st = (
        pl.scan_parquet(parquet_path)
        .with_columns(st.from_wkb("geometry").alias("geom"))
        .with_columns(
            pl.col("geom").st.geometry_type().alias("gtype"),
            pl.col("geom").st.area().alias("area"),
        )
        .filter(pl.col("area") > 0)
        .sort("area", descending=True)
        .select(["gtype", "area"])
        .head(10)
        .collect()
    )
    df_polars_st  # marimo renders the DataFrame
    return (df_polars_st,)


@app.cell
def __(pl):
    # Class A — geopolars is the early-alpha Rust-native polars-geo
    # crate (v0.1.0aN as of 2026-05; API still stabilizing). The cell
    # is intentionally minimal: it imports the package, captures the
    # version + public-attribute surface, and renders the inventory as
    # a DataFrame. Useful as a tripwire: when the alpha grows new
    # public functions, this cell's row count goes up and the user
    # can adopt them in subsequent notebooks. No load-bearing geometry
    # math — that's polars-st's lane until geopolars stabilizes.
    import geopolars as gpl
    _attrs = sorted(n for n in dir(gpl) if not n.startswith("_"))
    df_geopolars = pl.DataFrame({
        "field": ["version", "module"] + [f"api[{i}]" for i in range(len(_attrs))],
        "value": [
            str(getattr(gpl, "__version__", "unknown")),
            getattr(gpl, "__name__", "geopolars"),
        ] + _attrs,
    })
    df_geopolars  # marimo renders the info table
    return (df_geopolars,)


@app.cell
def __(mo, os):
    # Class C — lazy client-side fetch via MapLibre GL JS pointed at
    # martin's vector tiles. Why MapLibre, not folium?
    #   - tippecanoe → pmtiles → martin produces VECTOR tiles
    #     (Mapbox Vector Tile / PBF format).
    #   - folium's TileLayer is RASTER-only — it tries to render the
    #     PBF as a PNG, fails silently → grey map. Verified: martin
    #     returns Content-Type: application/x-protobuf for /monaco/
    #     {z}/{x}/{y}, NOT image/png.
    #   - MapLibre GL JS is the canonical client for vector tiles;
    #     it parses the TileJSON at /monaco, fetches tiles, and
    #     renders them client-side per the style spec below.
    #
    # The MARTIN_PUBLIC_URL env var must be set to the host-visible
    # martin URL (e.g. http://127.0.0.1:23000). MapLibre runs in the
    # USER'S browser, so all URLs in the embedded HTML must be
    # browser-reachable (NOT container-internal localhost:3000).
    # Martin reflects the marimo Origin in its CORS headers, so the
    # cross-port (22718 → 23000) XHR works without proxy tricks.
    martin = os.environ.get("MARTIN_PUBLIC_URL", "http://127.0.0.1:23000")
    # Terrain + hillshade DEM source: tiles.mapterhorn.com is the
    # canonical free terrarium-encoded raster-dem provider used by
    # MapLibre's own examples. CORS-permissive (Access-Control-
    # Allow-Origin: *) so works from any browser. The terrain config
    # elevates the entire scene; the hills layer renders relief
    # shading; the sky{} block adds atmospheric horizon for the
    # tilted pitch view.
    streets_html = f"""<!DOCTYPE html>
<html><head>
<link href="https://unpkg.com/maplibre-gl@5.24.0/dist/maplibre-gl.css" rel="stylesheet"/>
<script src="https://unpkg.com/maplibre-gl@5.24.0/dist/maplibre-gl.js"></script>
<style>html,body{{margin:0;padding:0;}}#map{{height:500px;width:100%;}}</style>
</head><body>
<div id="map"></div>
<script>
const map = new maplibregl.Map({{
  container: 'map',
  style: {{
    version: 8,
    sources: {{
      monaco: {{ type: 'vector', url: '{martin}/monaco' }},
      terrainSource: {{ type: 'raster-dem', url: 'https://tiles.mapterhorn.com/tilejson.json' }},
      hillshadeSource: {{ type: 'raster-dem', url: 'https://tiles.mapterhorn.com/tilejson.json' }}
    }},
    layers: [
      {{ id: 'bg', type: 'background',
         paint: {{ 'background-color': '#f0ece4' }} }},
      {{ id: 'hills', type: 'hillshade', source: 'hillshadeSource',
         layout: {{ visibility: 'visible' }},
         paint: {{ 'hillshade-shadow-color': '#473B24' }} }},
      {{ id: 'fill', type: 'fill', source: 'monaco', 'source-layer': 'monaco',
         filter: ['==', ['geometry-type'], 'Polygon'],
         paint: {{ 'fill-color': '#cfd5c0', 'fill-outline-color': '#7a7a7a',
                   'fill-opacity': 0.55 }} }},
      {{ id: 'line', type: 'line', source: 'monaco', 'source-layer': 'monaco',
         filter: ['==', ['geometry-type'], 'LineString'],
         paint: {{ 'line-color': '#444', 'line-width': 0.8 }} }},
      {{ id: 'circ', type: 'circle', source: 'monaco', 'source-layer': 'monaco',
         filter: ['==', ['geometry-type'], 'Point'],
         paint: {{ 'circle-color': '#c44', 'circle-radius': 1.5,
                   'circle-stroke-color': '#fff', 'circle-stroke-width': 0.5 }} }}
    ],
    terrain: {{ source: 'terrainSource', exaggeration: 1.5 }},
    sky: {{}}
  }},
  center: [7.4246, 43.7384],
  zoom: 14,
  pitch: 60,
  maxPitch: 85,
  attributionControl: false
}});
map.addControl(new maplibregl.NavigationControl({{ visualizePitch: true, showZoom: true, showCompass: true }}), 'top-right');
map.addControl(new maplibregl.TerrainControl({{ source: 'terrainSource', exaggeration: 1.5 }}), 'top-right');
</script>
</body></html>"""
    mo.iframe(streets_html, height="500px")


@app.cell
def __():
    # Helper exported to the three pipeline-comparison cells. Marimo's
    # reactive dataflow does not see module-level `def`s; the helper
    # must be DEFINED INSIDE a cell and CLAIMED as a dependency in the
    # signature of any cell that calls it. Cells 10/11/12 receive
    # `build_pipeline_maplibre_html` via their signature.
    def build_pipeline_maplibre_html(martin: str, source_name: str) -> str:
        """Shared MapLibre HTML template for the pipeline comparison cells.

        Each comparison map is a minimal 2D vector renderer pointed at
        one of the sibling PMTiles archives martin auto-discovers
        (monaco-gpqtiles, monaco-duckdb-mvt, monaco-duckdb-freestiler).
        The style is intentionally flat — no terrain / pitch / sky —
        so the visual difference between the four renderers (this one
        plus the streets cell above) is purely *what features each
        engine encoded into its tiles*, not styling tricks. Layer ID
        prefixes are source-name-suffixed so MapLibre's layer-id
        registry doesn't collide across cells.
        """
        layer_prefix = source_name.replace("monaco-", "")
        return f"""<!DOCTYPE html>
<html><head>
<link href="https://unpkg.com/maplibre-gl@5.24.0/dist/maplibre-gl.css" rel="stylesheet"/>
<script src="https://unpkg.com/maplibre-gl@5.24.0/dist/maplibre-gl.js"></script>
<style>html,body{{margin:0;padding:0;}}#map-{layer_prefix}{{height:400px;width:100%;}}</style>
</head><body>
<div id="map-{layer_prefix}"></div>
<script>
const map_{layer_prefix} = new maplibregl.Map({{
  container: 'map-{layer_prefix}',
  style: {{
    version: 8,
    sources: {{ src: {{ type: 'vector', url: '{martin}/{source_name}' }} }},
    layers: [
      {{ id: 'bg-{layer_prefix}', type: 'background',
         paint: {{ 'background-color': '#f6f3ec' }} }},
      {{ id: 'fill-{layer_prefix}', type: 'fill', source: 'src', 'source-layer': 'monaco',
         filter: ['==', ['geometry-type'], 'Polygon'],
         paint: {{ 'fill-color': '#a4c0a8', 'fill-outline-color': '#5e7060',
                   'fill-opacity': 0.55 }} }},
      {{ id: 'line-{layer_prefix}', type: 'line', source: 'src', 'source-layer': 'monaco',
         filter: ['==', ['geometry-type'], 'LineString'],
         paint: {{ 'line-color': '#3a3a3a', 'line-width': 0.8 }} }},
      {{ id: 'circ-{layer_prefix}', type: 'circle', source: 'src', 'source-layer': 'monaco',
         filter: ['==', ['geometry-type'], 'Point'],
         paint: {{ 'circle-color': '#b04a3d', 'circle-radius': 1.5 }} }}
    ]
  }},
  center: [7.4246, 43.7384],
  zoom: 13,
  attributionControl: false
}});
map_{layer_prefix}.addControl(new maplibregl.NavigationControl({{ showZoom: true, showCompass: true }}), 'top-right');
</script>
</body></html>"""
    return (build_pipeline_maplibre_html,)


@app.cell
def __(build_pipeline_maplibre_html, dag_run_states, mo, os):
    # Pipeline 2 — gpq-tiles direct GeoParquet → PMTiles. Renders the
    # sibling source `monaco-gpqtiles` that martin auto-discovers after
    # the gpqtiles DAG writes /workspace/tiles/pmtiles/monaco-gpqtiles.pmtiles.
    # Same vector-tile contract as the streets map above; styling is
    # deliberately neutral so visual differences = engine differences.
    assert dag_run_states["notebook_osm_gpqtiles_pipeline"] == "success"
    _martin = os.environ.get("MARTIN_PUBLIC_URL", "http://127.0.0.1:23000")
    mo.iframe(
        build_pipeline_maplibre_html(_martin, "monaco-gpqtiles"),
        height="400px",
    )


@app.cell
def __(build_pipeline_maplibre_html, dag_run_states, mo, os):
    # Pipeline 3 — DuckDB ST_AsMVT + pmtiles.Writer (hand-rolled per-tile
    # encoding in Python). Renders the sibling source `monaco-duckdb-mvt`
    # that martin auto-discovers after the DuckDB-MVT DAG completes.
    assert dag_run_states["notebook_osm_duckdb_mvt_pipeline"] == "success"
    _martin = os.environ.get("MARTIN_PUBLIC_URL", "http://127.0.0.1:23000")
    mo.iframe(
        build_pipeline_maplibre_html(_martin, "monaco-duckdb-mvt"),
        height="400px",
    )


@app.cell
def __(build_pipeline_maplibre_html, dag_run_states, mo, os):
    # Pipeline 4 — DuckDB → freestiler (Rust tiling engine takes the same
    # DuckDB SQL the AsMVT pipeline uses and produces a PMTiles archive
    # in one library call). Renders the sibling source
    # `monaco-duckdb-freestiler`. Compare side-by-side with the AsMVT
    # rendering above: same input, different engine.
    assert dag_run_states["notebook_osm_duckdb_freestiler_pipeline"] == "success"
    _martin = os.environ.get("MARTIN_PUBLIC_URL", "http://127.0.0.1:23000")
    mo.iframe(
        build_pipeline_maplibre_html(_martin, "monaco-duckdb-freestiler"),
        height="400px",
    )


@app.cell
def __(dag_run_states, pl, time):
    # cudf-polars GPU engine benchmark — RAPIDS cuDF-Polars (cu13)
    # plugs into Polars' LazyFrame.collect(engine=...) interface; the
    # GPU path executes the entire query plan via cuDF kernels.
    # https://docs.rapids.ai/api/cudf/stable/cudf_polars/
    #
    # The cell does three things:
    #   1. Probe CUDA availability via two independent signals
    #      (torch.cuda.is_available + a smoke LazyFrame collect with
    #      pl.GPUEngine). Discrepancies are reported, not hidden.
    #   2. Run an identical numeric group_by + agg twice — once on
    #      CPU, once on GPU — and time each via time.perf_counter().
    #   3. Emit a DataFrame so marimo renders the comparison as the
    #      cell's display value.
    #
    # Why a synthetic dataset: the OSM parquet's `geometry` column is
    # WKB Binary and the `tags` column is Map<str,str>; cudf-polars-cu13
    # does not support binary-element ops or map ops on GPU, so any
    # query touching those columns would either fail with
    # raise_on_fail=True or silently fall back to CPU with
    # raise_on_fail=False (defeating the benchmark's purpose). A
    # synthetic 2M-row numeric frame keeps the entire query on the GPU
    # so the timing measures real GPU execution.
    #
    # Gates on dag_run_states["notebook_osm_pipeline"] only so the
    # cell positions sequentially after the OSM DAG completes — the
    # benchmark itself doesn't consume any OSM artifact.
    assert dag_run_states["notebook_osm_pipeline"] == "success"

    # --- 1. Availability probes ---
    try:
        import torch
        _torch_cuda = bool(torch.cuda.is_available())
        _torch_devices = int(torch.cuda.device_count()) if _torch_cuda else 0
    except Exception as _e:
        _torch_cuda, _torch_devices = False, 0

    _gpu_engine_ok = False
    _gpu_engine_err = ""
    try:
        _smoke = pl.LazyFrame({"a": [1, 2, 3]}).select(
            pl.col("a").sum().alias("s")
        ).collect(engine=pl.GPUEngine(raise_on_fail=True))
        assert int(_smoke["s"][0]) == 6
        _gpu_engine_ok = True
    except Exception as _e:
        _gpu_engine_err = f"{type(_e).__name__}: {_e}"[:160]

    # --- 2. Bench against a synthetic 2M-row numeric dataset ---
    import numpy as np
    _rng = np.random.default_rng(seed=42)
    _n = 2_000_000
    _bench_src = pl.DataFrame({
        "group": _rng.integers(0, 1000, _n, dtype=np.int32),
        "value": _rng.standard_normal(_n).astype(np.float64),
    })
    _query = (
        _bench_src.lazy()
        .group_by("group")
        .agg([
            pl.col("value").sum().alias("sum"),
            pl.col("value").mean().alias("mean"),
            pl.col("value").max().alias("max"),
            pl.len().alias("n"),
        ])
        .sort("group")
    )

    _cpu_start = time.perf_counter()
    _df_cpu = _query.collect()
    _cpu_elapsed = time.perf_counter() - _cpu_start

    if _gpu_engine_ok:
        # raise_on_fail=True forces real GPU execution — no silent
        # CPU fallback. Numeric group_by + sum/mean/max are fully
        # supported by cudf-polars-cu13.
        _gpu_start = time.perf_counter()
        _df_gpu = _query.collect(engine=pl.GPUEngine(raise_on_fail=True))
        _gpu_elapsed = time.perf_counter() - _gpu_start
        _gpu_rows = _df_gpu.height
    else:
        _gpu_elapsed = float("nan")
        _gpu_rows = -1

    # --- 3. Render the comparison DataFrame ---
    df_cudf_polars_bench = pl.DataFrame({
        "engine":         ["cpu", "gpu" if _gpu_engine_ok else "gpu (unavailable)"],
        "elapsed_seconds": [_cpu_elapsed, _gpu_elapsed],
        "rows":            [_df_cpu.height, _gpu_rows],
        "note":            [
            f"torch.cuda.is_available={_torch_cuda} (devices={_torch_devices})",
            "ok" if _gpu_engine_ok else _gpu_engine_err or "GPU engine init failed",
        ],
    })
    df_cudf_polars_bench  # marimo renders the comparison
    return (df_cudf_polars_bench,)


@app.cell
def __(dag_run_states, os, pl):
    # Class A — server-side polars on the GTFS parquet directory
    # produced by notebook_gtfs_pipeline. Reports stop / route counts
    # plus the top routes by stop count (a useful "where does each
    # bus go?" summary for Monaco's compact transit network).
    assert dag_run_states["notebook_gtfs_pipeline"] == "success"
    gtfs_dir = os.path.expanduser("/workspace/gtfs/parquet")

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
