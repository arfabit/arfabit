# ARFABIT

Convert a disc to natively play on Apple TV.

ARFABIT watches your disc drive. When you put in a DVD or Blu-ray, it copies the disc, makes a movie file your Apple TV plays without any fuss, reads the subtitles into text, gives everything a proper name, and puts it in your movie folder. You run it from your web browser, on any device in your house.

> ARFABIT is new. If something doesn't work the way this page says, please [open an issue](https://github.com/arfabit/arfabit/issues).

## What you need

- A computer running macOS, Windows, or Linux
- A DVD or Blu-ray drive, plugged in or built in
- Two free programs: **MakeMKV** and **FFmpeg**

When you first open ARFABIT, it checks all of this and tells you what's missing and how to get it.

## Getting started

1. **Build and run ARFABIT.** There are no downloads yet. With Go 1.27 or later: `go build ./cmd/arfabit && ./arfabit`
2. **Your browser opens** to the ARFABIT page.
3. **Follow the checklist** at the top, if there is one.
4. **Put a disc in.**
5. **Press "Plan"** under your drive. ARFABIT reads the disc and shows you what it found.
6. **Press "Start".**

The disc comes out on its own as soon as it has been copied, so the next one can go in while the last one is still being made into a movie.

### Letting it run on its own

Under your drive, **When a disc goes in** can be set to:

- **Nothing**: wait for you to press Plan.
- **Copy**: copy every disc you put in, and read its subtitles. Nothing else.
- **Blueprint**: copy every disc you put in and make the movie too, the way that blueprint says.

With Copy or Blueprint, you only have to feed it discs. You can still change the plan for a disc while it's being copied.

To have ARFABIT start whenever your computer does, turn on "Start ARFABIT when this computer starts" in Settings. To stop it, press **Stop ARFABIT** in Settings, or Ctrl+C in the window you started it from.

## The page

There are four sections, along the bottom:

- **Tasks** is what's happening: your drive, what's working and waiting, the log, and everything that's finished.
- **Projects** is where you decide what to make. Pick a disc, or anything already in your library, then choose which video, audio, and subtitle tracks to keep, and how.
- **Blueprints** are recipes, so you don't have to choose the same things every time. Make one called "Small" with a lower quality, say, and use it whenever you like.
- **Settings** is everything else.

The suggestions ARFABIT makes are good ones. You can change anything, or just press Start.

A movie takes a few hours. You can close the browser and come back later. Nothing stops when you walk away.

## Where your movies go

Everything goes in your Downloads folder, in a folder called `arfabit`, one folder per movie:

```
Downloads/arfabit/
  Blade Runner (1982)/
    Blade Runner (1982) {edition-Original}.mkv      the disc, exactly as it was
    Blade Runner (1982) {edition-Original}.en.srt   its subtitles, as text
    Blade Runner (1982).mkv                         the movie for your TV
    Blade Runner (1982).en.srt                      its subtitles
```

Point Plex, Infuse, or Jellyfin at the `arfabit` folder. It's named the way they expect, so they pick everything up without any setup.

Each movie's folder holds the **original**: an exact copy of the disc. It's large. ARFABIT keeps it so you never have to copy the same disc again: you can make another version of the movie from it any time, in Projects. Plex shows it as one more version of the movie, called Original.

ARFABIT never removes anything. When you want the space back, move the original to the trash yourself. If a disc won't fit, ARFABIT tells you before it starts, and shows you how much room your originals and your movies take.

## Subtitles

A Blu-ray's subtitles are pictures, and Plex can only show pictures by converting the whole movie as it plays. So ARFABIT reads them into text, using the text reader your computer already has. On Linux, that means Tesseract, if you've installed it.

Reading isn't perfect. ARFABIT marks every line it's unsure of, and shows it to you next to the picture it came from, so you can pick the right words. Any subtitles still waiting for a look are listed at the top of Tasks, however long ago the movie was made. When you fix a line, ARFABIT offers to update the movie's subtitles too.

## Trying out settings

In Projects, set **Length** to a minute or so and make a few short pieces of the same movie, each with a different edition name and quality. Watch them on your TV and keep the one you like. ARFABIT shows how big each would be for the whole movie, and how long it would take.

## Why this exists

There are other programs that do this. They work, and we learned from them.

ARFABIT exists because of a few small frustrations:

- Reading the logs was hard. Pages refreshed while you were searching them.
- Blu-ray and 4K discs got treated the same, so you couldn't set them up differently.
- Changing quality for one disc meant changing it for every disc.
- Subtitles read into text had mistakes, and nothing helped you find them.

So ARFABIT keeps the logs still, tells 4K apart from Blu-ray, lets you change anything for one disc, and points you to every subtitle line it's unsure of.

## If something goes wrong

ARFABIT tells you what happened in plain language, and shows you exactly what the underlying program reported if you want to see it. It never guesses at a cause it isn't sure about.

A problem never touches your files. A disc that doesn't finish leaves everything else exactly where it was.

Questions and problems: [open an issue](https://github.com/arfabit/arfabit/issues).

MIT licensed.
