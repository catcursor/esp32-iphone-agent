$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
try {
    Add-Type -AssemblyName System.Runtime.WindowsRuntime
    $storage = [Windows.Storage.StorageFile,Windows.Storage,ContentType=WindowsRuntime]
    $streamType = [Windows.Storage.Streams.IRandomAccessStream,Windows.Storage.Streams,ContentType=WindowsRuntime]
    $decoderType = [Windows.Graphics.Imaging.BitmapDecoder,Windows.Graphics.Imaging,ContentType=WindowsRuntime]
    $bitmapType = [Windows.Graphics.Imaging.SoftwareBitmap,Windows.Graphics.Imaging,ContentType=WindowsRuntime]
    $engineType = [Windows.Media.Ocr.OcrEngine,Windows.Foundation,ContentType=WindowsRuntime]
    $resultType = [Windows.Media.Ocr.OcrResult,Windows.Foundation,ContentType=WindowsRuntime]
    $asTask = [System.WindowsRuntimeSystemExtensions].GetMethods() |
        Where-Object { $_.Name -eq 'AsTask' -and $_.IsGenericMethod -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1' } |
        Select-Object -First 1
    function Await-Operation($operation, [type]$type) {
        $task = $asTask.MakeGenericMethod($type).Invoke($null, @($operation))
        $task.Wait()
        $task.Result
    }
    $scriptTag = $env:ESP32AGENT_OCR_SCRIPT
    if ($scriptTag) {
        $language = $engineType::AvailableRecognizerLanguages |
            Where-Object { $_.LanguageTag -like ($scriptTag + '*') } | Select-Object -First 1
        if (!$language) {
            [Console]::Error.WriteLine('Install the Windows OCR language feature for ' + $scriptTag)
            exit 3
        }
        $engine = $engineType::TryCreateFromLanguage($language)
    } else {
        $engine = $engineType::TryCreateFromUserProfileLanguages()
    }
    if (!$engine) {
        [Console]::Error.WriteLine('Install the Windows OCR language feature for the screenshot language')
        exit 3
    }
    $file = Await-Operation ($storage::GetFileFromPathAsync($env:ESP32AGENT_OCR_INPUT)) $storage
    $stream = Await-Operation ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) $streamType
    try {
        $decoder = Await-Operation ($decoderType::CreateAsync($stream)) $decoderType
        $bitmap = Await-Operation ($decoder.GetSoftwareBitmapAsync()) $bitmapType
        try { $result = Await-Operation ($engine.RecognizeAsync($bitmap)) $resultType }
        finally { $bitmap.Dispose() }
    } finally { $stream.Dispose() }
    $lines = @(foreach ($line in $result.Lines) {
        ,@(foreach ($item in $line.Words) {
            $r = $item.BoundingRect
            [pscustomobject]@{
                Text = $item.Text
                Box = @{
                    Min = @{X=[int][math]::Floor($r.X); Y=[int][math]::Floor($r.Y)}
                    Max = @{X=[int][math]::Ceiling($r.X+$r.Width); Y=[int][math]::Ceiling($r.Y+$r.Height)}
                }
            }
        })
    })
    ConvertTo-Json -InputObject $lines -Depth 8 -Compress
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
