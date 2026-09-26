# ARFABIT

Convert a disc to natively play on Apple TV.

ARFABIT watches your disc drive. When you put in a DVD or Blu-ray, it copies the movie off the disc, converts it to a file your Apple TV understands, gives it a proper name, and puts it in your movie folder. You run it from your web browser, from any device in your house.

> **Early days.** ARFABIT works, but it is new. One thing is still missing: it cannot yet turn a disc's subtitles into text, so your movie will not have subtitles. Everything else below works. The original copy of the disc is kept, so nothing is lost by starting now.

## What you need

- A computer running macOS, Windows, or Linux
- A DVD or Blu-ray drive, plugged in or built in
- Two free programs: **MakeMKV** and **FFmpeg**

When you first open ARFABIT, it checks the above and guides you.

## Getting started

1. **Download ARFABIT** for your computer and open it.
2. **Your browser opens** to the ARFABIT page. Leave it open.
3. **Follow the checklist.** ARFABIT tells you if anything is missing and how to fix it.
4. **Put a disc in.**
5. **Press "Plan".** ARFABIT finds the movie and shows you what it found.
6. **Press "Start".**

The disc pops out on its own as soon as it has been copied, so the next one can go in while the last one is still being converted.

To have ARFABIT start up whenever your computer does, flip the switch in Settings that says "Start ARFABIT when this computer starts."

To stop it, use the **Stop ARFABIT** button in Settings, or press Ctrl+C in the window you started it from. Starting it again while a copy is already running simply takes over from the old one.

## While it's working

ARFABIT shows you one page with everything on it:

- **What movie it found**, and how long it thinks it will take
- **Choices you can change** before it starts, like whether to keep subtitles
- **How much space** each choice will use

You can change anything, or just let it go. The suggestions are already good ones.

A movie takes a few hours. You can close the browser and come back later. Nothing stops when you walk away.

## Where your movies go

Everything lands in your Downloads folder, in a folder called `arfabit`.

```
Downloads/arfabit/
  library/                        your movies
    Blade Runner (1982)/
      Blade Runner (1982) {edition-Original}.mkv     the disc, exactly as it was
      Blade Runner (1982).mkv                        the movie for your TV
  clips/                          short clips, for choosing a quality
```

The **library** folder is the one you want. It's named the way Plex, Infuse, and Jellyfin expect, so those apps pick it up without any setup.

Each movie's folder also holds the **original**: an exact copy of what was on the disc. It's large. ARFABIT keeps it so you never have to copy the same disc twice, and it never removes anything on its own. Plex shows it as one more version of the movie, called Original. When you want the space back, move that one file to the trash yourself.

If you used ARFABIT before, your earlier copies are still in a folder called `masters`, and ARFABIT still makes movies from them. It doesn't move them.

If a disc won't fit, ARFABIT tells you before it starts, and shows you how much room your originals and your movies take.

## Why this exists

There are other programs that do this. They work, and we learned from them.

ARFABIT exists because of a few small frustrations:

- Reading the logs was hard. Pages refreshed while you were searching them.
- Blu-ray and 4K discs got treated the same, so you couldn't set them up differently.
- Changing quality settings for one disc meant editing settings for all discs.

So ARFABIT keeps the logs still, tells 4K apart from Blu-ray, lets you switch quality per disc with one click.

## If something goes wrong

ARFABIT tells you what happened in plain language, and shows you exactly what the underlying program reported if you want to see it. It never guesses at a cause it isn't sure about.

Your files are never touched by a problem. A disc that doesn't finish leaves everything else exactly where it was.

Questions and problems: [open an issue](https://github.com/arfabit/arfabit/issues).

MIT licensed.
