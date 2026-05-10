#import <AVFoundation/AVFoundation.h>
#import <jni.h>
#import <stdlib.h>
#import <string.h>

#ifdef __cplusplus
extern "C" {
#endif

static JavaVM*            jvm         = NULL;
static jobject            gCallback   = NULL;
static jmethodID          gOnFrame    = NULL;
static AVAudioEngine*     engine      = nil;
static AVAudioPlayerNode* playerNode  = nil;
// Capture: resample hw → 16 kHz mono
static AVAudioConverter*  gCapConv    = nil;
static AVAudioFormat*     gTargetFmt  = nil;
// Playback: upsample TTS → hw rate
static AVAudioFormat*     gPlayFmt    = nil;
// Accumulate converted capture samples into exact-frameSize chunks
static float*             gAccum      = NULL;
static int                gAccumSize  = 0;
static int                gFrameSize  = 512;
// Echo suppression: mute mic while speaker is active + tail
static volatile BOOL      gIsPlaying  = NO;

JNIEXPORT jint JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_startCapture(
        JNIEnv* env, jclass cls, jobject callback, jint sampleRate, jint frameSize) {
    if (engine) {
        [[engine inputNode] removeTapOnBus:0];
        [engine stop];
        engine = nil;
    }
    playerNode = nil;
    gCapConv = nil; gTargetFmt = nil; gPlayFmt = nil;
    free(gAccum); gAccum = NULL; gAccumSize = 0;
    if (gCallback) { env->DeleteGlobalRef(gCallback); gCallback = NULL; }

    gFrameSize = (int)frameSize;
    env->GetJavaVM(&jvm);
    gCallback = env->NewGlobalRef(callback);
    jclass cbClass = env->GetObjectClass(gCallback);
    gOnFrame = env->GetMethodID(cbClass, "onFrame", "([F)V");

    engine = [[AVAudioEngine alloc] init];

    // VoiceProcessingIO (setVoiceProcessingEnabled) requires an audio session context
    // that macOS command-line JVM processes don't have — it consistently fails with
    // -10875. Instead we suppress echo by muting the mic during playback + a short tail.
    AVAudioInputNode* inputNode = [engine inputNode];

    // With VP on the input reports 5 ch (VoiceProcessingIO internal layout) at
    // the hardware rate. The output path must also be at the hardware rate.
    // Query hw rate AFTER enabling VP so we get the real VP session rate.
    double hwRate = [[engine outputNode] outputFormatForBus:0].sampleRate;
    if (hwRate < 8000) hwRate = 48000.0;  // fallback
    NSLog(@"[AVFoundationJNI] hw rate: %.0f Hz (echo suppression via playback mute)", hwRate);

    // Playback connection at hw rate mono — VoiceProcessingIO requires the
    // output path to run at hardware sample rate. Scheduling 24 kHz TTS buffers
    // is allowed; AVAudioPlayerNode converts on the fly.
    gPlayFmt = [[AVAudioFormat alloc]
        initWithCommonFormat:AVAudioPCMFormatFloat32
                  sampleRate:hwRate
                    channels:1
                 interleaved:NO];
    playerNode = [[AVAudioPlayerNode alloc] init];
    [engine attachNode:playerNode];
    [engine connect:playerNode to:[engine mainMixerNode] format:gPlayFmt];

    // Build capture → 16 kHz mono converter.
    // Channel 0 of the VP tap is the AEC-processed mic signal.
    gTargetFmt = [[AVAudioFormat alloc]
        initWithCommonFormat:AVAudioPCMFormatFloat32
                  sampleRate:(double)sampleRate
                    channels:1
                 interleaved:NO];
    // We'll build gCapConv lazily in the tap after we see the actual hw format,
    // because the format is only final after the engine starts.
    gAccum = (float*)malloc((size_t)(frameSize * 8) * sizeof(float));

    jobject   localCB  = gCallback;
    jmethodID localMID = gOnFrame;

    AVAudioFrameCount tapBufSize = (AVAudioFrameCount)(frameSize * hwRate / sampleRate);

    [inputNode installTapOnBus:0
                    bufferSize:tapBufSize
                        format:nil
                         block:^(AVAudioPCMBuffer* hwBuf, AVAudioTime* when) {
        // Suppress mic during playback to prevent echo
        if (gIsPlaying) return;

        // Build converter on first call (real hw format known after engine start)
        if (!gCapConv) {
            // VP input is N-channel; we only care about ch 0 (AEC-processed mic).
            // Create a 1-ch view at the same sample rate, then resample to target.
            AVAudioFormat* mono48 = [[AVAudioFormat alloc]
                initWithCommonFormat:AVAudioPCMFormatFloat32
                          sampleRate:hwBuf.format.sampleRate
                            channels:1
                         interleaved:NO];
            gCapConv = [[AVAudioConverter alloc] initFromFormat:mono48 toFormat:gTargetFmt];
            NSLog(@"[AVFoundationJNI] capture tap format: %@, ch0→16k converter ready", hwBuf.format);
        }

        // Extract channel 0 (AEC-processed mic) into a 1-ch buffer
        AVAudioFrameCount frames = hwBuf.frameLength;
        AVAudioFormat* mono48 = [[AVAudioFormat alloc]
            initWithCommonFormat:AVAudioPCMFormatFloat32
                      sampleRate:hwBuf.format.sampleRate
                        channels:1
                     interleaved:NO];
        AVAudioPCMBuffer* ch0Buf = [[AVAudioPCMBuffer alloc]
            initWithPCMFormat:mono48 frameCapacity:frames];
        ch0Buf.frameLength = frames;
        memcpy(ch0Buf.floatChannelData[0], hwBuf.floatChannelData[0], frames * sizeof(float));

        // Resample to target rate
        AVAudioFrameCount outCap = (AVAudioFrameCount)(frames * gTargetFmt.sampleRate / mono48.sampleRate) + 8;
        AVAudioPCMBuffer* outBuf = [[AVAudioPCMBuffer alloc]
            initWithPCMFormat:gTargetFmt frameCapacity:outCap];
        __block BOOL consumed = NO;
        NSError* convErr = nil;
        [gCapConv convertToBuffer:outBuf error:&convErr withInputFromBlock:
            ^AVAudioBuffer*(AVAudioPacketCount n, AVAudioConverterInputStatus* status) {
                if (!consumed) { consumed = YES; *status = AVAudioConverterInputStatus_HaveData; return ch0Buf; }
                *status = AVAudioConverterInputStatus_NoDataNow; return nil;
            }];
        if (convErr || outBuf.frameLength == 0) return;

        // Accumulate and deliver exact frameSize chunks to JVM
        float* samples  = outBuf.floatChannelData[0];
        int    remaining = (int)outBuf.frameLength;
        while (remaining > 0) {
            int space = gFrameSize - gAccumSize;
            int copy  = remaining < space ? remaining : space;
            memcpy(gAccum + gAccumSize, samples, (size_t)copy * sizeof(float));
            gAccumSize += copy;
            samples    += copy;
            remaining  -= copy;
            if (gAccumSize < gFrameSize) break;

            JNIEnv* tapEnv = NULL;
            jint ar = jvm->GetEnv((void**)&tapEnv, JNI_VERSION_1_6);
            bool didAttach = false;
            if (ar == JNI_EDETACHED) {
                if (jvm->AttachCurrentThread((void**)&tapEnv, NULL) != JNI_OK) { gAccumSize = 0; return; }
                didAttach = true;
            } else if (ar != JNI_OK) { gAccumSize = 0; return; }

            jfloatArray arr = tapEnv->NewFloatArray(gFrameSize);
            tapEnv->SetFloatArrayRegion(arr, 0, gFrameSize, gAccum);
            tapEnv->CallVoidMethod(localCB, localMID, arr);
            tapEnv->DeleteLocalRef(arr);
            if (didAttach) jvm->DetachCurrentThread();
            gAccumSize = 0;
        }
    }];

    NSError* startErr = nil;
    if (![engine startAndReturnError:&startErr]) {
        NSLog(@"[AVFoundationJNI] engine start failed: %@", startErr);
        [[engine inputNode] removeTapOnBus:0];
        engine = nil; playerNode = nil;
        env->DeleteGlobalRef(gCallback); gCallback = NULL;
        return -1;
    }
    NSLog(@"[AVFoundationJNI] started — echo suppression active");
    [playerNode play];
    return 0;
}

JNIEXPORT void JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_stopCapture(
        JNIEnv* env, jclass cls) {
    if (engine) {
        [[engine inputNode] removeTapOnBus:0];
        [engine stop];
        engine = nil;
    }
    playerNode = nil;
    gCapConv = nil; gTargetFmt = nil; gPlayFmt = nil;
    free(gAccum); gAccum = NULL; gAccumSize = 0;
    if (gCallback) { env->DeleteGlobalRef(gCallback); gCallback = NULL; }
}

JNIEXPORT jint JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_playSamples(
        JNIEnv* env, jclass cls, jfloatArray samples, jint sampleRate) {
    if (!playerNode || !engine || !gPlayFmt) return -1;

    jsize   len = env->GetArrayLength(samples);
    jfloat* src = env->GetFloatArrayElements(samples, NULL);

    // TTS format (e.g. 24 kHz mono)
    AVAudioFormat* inFmt = [[AVAudioFormat alloc]
        initWithCommonFormat:AVAudioPCMFormatFloat32
                  sampleRate:(double)sampleRate
                    channels:1
                 interleaved:NO];
    AVAudioPCMBuffer* inBuf = [[AVAudioPCMBuffer alloc]
        initWithPCMFormat:inFmt frameCapacity:(AVAudioFrameCount)len];
    inBuf.frameLength = (AVAudioFrameCount)len;
    memcpy(inBuf.floatChannelData[0], src, (size_t)len * sizeof(float));
    env->ReleaseFloatArrayElements(samples, src, JNI_ABORT);

    // Convert to hw rate if needed so the buffer matches the playerNode connection format
    AVAudioPCMBuffer* playBuf = inBuf;
    if ((jint)gPlayFmt.sampleRate != sampleRate) {
        AVAudioConverter* conv = [[AVAudioConverter alloc] initFromFormat:inFmt toFormat:gPlayFmt];
        AVAudioFrameCount outCap = (AVAudioFrameCount)(len * gPlayFmt.sampleRate / sampleRate) + 8;
        playBuf = [[AVAudioPCMBuffer alloc] initWithPCMFormat:gPlayFmt frameCapacity:outCap];
        __block BOOL consumed = NO;
        NSError* convErr = nil;
        [conv convertToBuffer:playBuf error:&convErr withInputFromBlock:
            ^AVAudioBuffer*(AVAudioPacketCount n, AVAudioConverterInputStatus* status) {
                if (!consumed) { consumed = YES; *status = AVAudioConverterInputStatus_HaveData; return inBuf; }
                *status = AVAudioConverterInputStatus_NoDataNow; return nil;
            }];
        if (convErr || playBuf.frameLength == 0) return -1;
    }

    gIsPlaying = YES;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    [playerNode scheduleBuffer:playBuf completionHandler:^{
        // Keep mic muted for 300 ms after playback ends to absorb speaker tail
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 300 * NSEC_PER_MSEC),
                       dispatch_get_global_queue(QOS_CLASS_DEFAULT, 0), ^{
            gIsPlaying = NO;
        });
        dispatch_semaphore_signal(sem);
    }];
    dispatch_time_t timeout = dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC);
    if (dispatch_semaphore_wait(sem, timeout) != 0) { gIsPlaying = NO; return -1; }
    return 0;
}

JNIEXPORT void JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_stopPlayback(
        JNIEnv* env, jclass cls) {
    if (playerNode) [playerNode stop];
}

#ifdef __cplusplus
}
#endif
