# Reads the text in subtitle pictures with Windows.Media.Ocr, from Windows
# PowerShell 5.1.
#
#   powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File windows.ps1 <list> <answer> [language]
#
# <list> holds one PNG path per line. The answer is JSON: a line per picture,
# in the same order, or {"missing": true} when no installed language pack
# reads the language asked for. language is a tag such as "en", matched
# against the start of the tags Windows names its languages by ("en-US").
param([string]$List, [string]$Out, [string]$Lang = '')

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$null = [Windows.Storage.StorageFile, Windows.Storage, ContentType = WindowsRuntime]
$null = [Windows.Storage.Streams.IRandomAccessStream, Windows.Storage.Streams, ContentType = WindowsRuntime]
$null = [Windows.Graphics.Imaging.BitmapDecoder, Windows.Graphics, ContentType = WindowsRuntime]
$null = [Windows.Graphics.Imaging.SoftwareBitmap, Windows.Graphics, ContentType = WindowsRuntime]
$null = [Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType = WindowsRuntime]

# WinRT calls are asynchronous; AsTask turns one into something to wait on.
$asTask = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
  $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and
  $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1'
} | Select-Object -First 1

function Await($operation, [Type]$type) {
  $task = $asTask.MakeGenericMethod($type).Invoke($null, @($operation))
  $task.Wait() | Out-Null
  $task.Result
}

function Write-Answer($value) {
  $json = ConvertTo-Json -InputObject $value -Depth 4 -Compress
  [System.IO.File]::WriteAllText($Out, $json, (New-Object System.Text.UTF8Encoding $false))
}

$engine = $null
if ($Lang) {
  foreach ($l in [Windows.Media.Ocr.OcrEngine]::AvailableRecognizerLanguages) {
    $t = $l.LanguageTag
    if ($t -eq $Lang -or $t.StartsWith("$Lang-", [StringComparison]::OrdinalIgnoreCase)) {
      $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($l)
      if ($engine) { break }
    }
  }
  if (-not $engine) {
    Write-Answer @{ missing = $true }
    exit 0
  }
} else {
  $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages()
  if (-not $engine) {
    Write-Answer @{ missing = $true }
    exit 0
  }
}

$lines = New-Object System.Collections.ArrayList
foreach ($path in [System.IO.File]::ReadAllLines($List)) {
  if (-not $path) { continue }
  $file = Await ([Windows.Storage.StorageFile]::GetFileFromPathAsync($path)) ([Windows.Storage.StorageFile])
  $stream = Await ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
  try {
    $decoder = Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
    $bitmap = Await ($decoder.GetSoftwareBitmapAsync([Windows.Graphics.Imaging.BitmapPixelFormat]::Bgra8, [Windows.Graphics.Imaging.BitmapAlphaMode]::Premultiplied)) ([Windows.Graphics.Imaging.SoftwareBitmap])
    $result = Await ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
    $text = (@($result.Lines | ForEach-Object { $_.Text }) -join "`n")
    [void]$lines.Add(@{ text = $text })
    $bitmap.Dispose()
  } finally {
    $stream.Dispose()
  }
}

Write-Answer @{ lines = $lines.ToArray() }
