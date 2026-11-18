# World boundaries

Natural Earth 5.1.2, 1:110m admin 0 countries, public domain.

Source: https://github.com/nvkelso/natural-earth-vector/blob/v5.1.2/geojson/ne_110m_admin_0_countries.geojson

The bundled GeoJSON preserves source geometries and keeps only display names and
country-code mapping (`ISO_A2_EH`, then `ISO_A2`, accepting two-letter values).
`geometry_id` is the independent source `NE_ID`, prefixed with `ne:`; D3 keys
geometries by this ID rather than by a country code. Postal abbreviations are
not ISO mappings: this source supplies neither ISO field for Somaliland and
Northern Cyprus, so those geometries keep an empty code and no country shading.
France and Norway retain their source `ISO_A2_EH` values, `FR` and `NO`.
This records the dataset's fields without inferring territorial ownership.
The original GeoJSON SHA-256 is
`6866c877d39cba9c357620878839b336d569f8c662d3cfab4cb1dbe2d39c977f`.
Country geometry
is a visualization base. Coordinates are supplied by the collected FOFA records;
unknown coordinates are never placed at a country centroid.
