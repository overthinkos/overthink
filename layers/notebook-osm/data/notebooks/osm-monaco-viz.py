import marimo

__generated_with = "0.16.0"
app = marimo.App(width="medium")


@app.cell
def __():
    import polars as pl
    import folium
    return pl, folium


@app.cell
def __(pl):
    # Demonstrate Polars-GPU on the parquet output of the
    # osm-monaco-pipeline DAG. collect(engine="gpu") routes through
    # cudf-polars-cu13; falls back to CPU automatically if any operator
    # isn't GPU-accelerated.
    import os
    parquet_path = os.path.expanduser("~/workspace/tiles/work/monaco.parquet")
    df = (
        pl.scan_parquet(parquet_path)
        .group_by("tags")
        .agg(pl.len().alias("n"))
        .sort("n", descending=True)
        .head(20)
        .collect(engine="gpu")
    )
    return (df,)


@app.cell
def __(folium):
    # Render the Monaco PMTiles served by martin (in-pod localhost:3000).
    m = folium.Map(location=[43.7384, 7.4246], zoom_start=14, tiles=None)
    folium.TileLayer(
        tiles="http://localhost:3000/monaco/{z}/{x}/{y}",
        attr="OSM via QuackOSM + tippecanoe + martin",
        name="Monaco OSM",
    ).add_to(m)
    return (m,)


if __name__ == "__main__":
    app.run()
