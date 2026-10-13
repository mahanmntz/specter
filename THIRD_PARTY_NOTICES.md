# Third-party notices

Specter's web UI embeds the following open-source assets. Their licenses require that these notices ship with the software.

## Inter (typeface)
`internal/web/static/vendor/inter-latin-wght-normal.woff2`, from `@fontsource-variable/inter`.
Copyright 2016 The Inter Project Authors (https://github.com/rsms/inter). Licensed under the SIL Open Font License, Version 1.1 (https://openfontlicense.org).

## Radix Colors
The color scales used for the light and dark themes in `internal/web/static/style.css`.
MIT License. Copyright (c) 2021 Radix. https://www.radix-ui.com/colors

## Lucide (icons)
`internal/web/static/icons.svg` is a sprite built from Lucide icons (https://lucide.dev).
ISC License. Copyright (c) for portions of Lucide are held by Cole Bemis 2013-2022 as part of Feather (MIT). All other copyright (c) for Lucide are held by Lucide Contributors 2022.
Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted, provided that the above copyright notice and this permission notice appear in all copies.

## Data sources (used at run time, not bundled)
- remoteintech/remote-jobs directory (ISC): downloaded on the user's machine by `specter seeds import remoteintech`.
- Public job APIs: Remotive (attribution required, rate limited), Jobicy, Himalayas, Arbeitnow, RemoteOK, Hacker News (Algolia). Please follow each provider's terms.

## ledongthuc/pdf (Go library)
Used to read text from PDF CVs, locally and in memory. BSD 3-Clause License. Copyright (c) 2009 The Go Authors. All rights reserved. https://github.com/ledongthuc/pdf
