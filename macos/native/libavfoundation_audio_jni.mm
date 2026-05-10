#import <AVFoundation/AVFoundation.h>
#import <jni.h>
#import <stdlib.h>
#import <string.h>

static JavaVM*            jvm        = NULL;
static jobject            gCallback  = NULL;
static jmethodID          gOnFrame   = NULL;
static AVAudioEngine*     engine     = nil;
static AVAudioPlayerNode* playerNode = nil;

JNIEXPORT jint JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_startCapture(
        JNIEnv* env, jclass cls, jobject callback, jint sampleRate, jint frameSize) {
    // Fix 4: tear down any existing state before reinitializing to avoid leaks on double-call
    if (engine) {
        [[engine inputNode] removeTapOnBus:0];
        [engine stop];
        engine = nil;
    }
    playerNode = nil;
    if (gCallback) { env->DeleteGlobalRef(gCallback); gCallback = NULL; }

    env->GetJavaVM(&jvm);
    gCallback = env->NewGlobalRef(callback);
    jclass cbClass = env->GetObjectClass(gCallback);
    gOnFrame = env->GetMethodID(cbClass, "onFrame", "([F)V");

    engine     = [[AVAudioEngine alloc] init];
    playerNode = [[AVAudioPlayerNode alloc] init];
    [engine attachNode:playerNode];
    [engine connect:playerNode to:[engine mainMixerNode] format:nil];

    AVAudioInputNode* inputNode = [engine inputNode];
    NSError* err = nil;
    if (![inputNode setVoiceProcessingEnabled:YES error:&err]) {
        NSLog(@"[AVFoundationJNI] voice processing unavailable: %@", err);
    }

    AVAudioFormat* captureFmt = [[AVAudioFormat alloc]
        initWithCommonFormat:AVAudioPCMFormatFloat32
                  sampleRate:(double)sampleRate
                    channels:1
                 interleaved:NO];

    // Fix 1: capture local copies before installing tap to avoid race with stopCapture
    jobject   localCB  = gCallback;
    jmethodID localMID = gOnFrame;

    [inputNode installTapOnBus:0
                    bufferSize:(AVAudioFrameCount)frameSize
                        format:captureFmt
                         block:^(AVAudioPCMBuffer* buf, AVAudioTime* when) {
        float* data  = buf.floatChannelData[0];
        jsize  count = (jsize)buf.frameLength;
        // Fix 2: check attach state; only detach if this call attached
        JNIEnv* tapEnv = NULL;
        jint attachResult = jvm->GetEnv((void**)&tapEnv, JNI_VERSION_1_6);
        bool didAttach = false;
        if (attachResult == JNI_EDETACHED) {
            if (jvm->AttachCurrentThread((void**)&tapEnv, NULL) != JNI_OK) return;
            didAttach = true;
        } else if (attachResult != JNI_OK) {
            return;
        }
        jfloatArray arr = tapEnv->NewFloatArray(count);
        tapEnv->SetFloatArrayRegion(arr, 0, count, data);
        tapEnv->CallVoidMethod(localCB, localMID, arr);
        tapEnv->DeleteLocalRef(arr);
        if (didAttach) jvm->DetachCurrentThread();
    }];

    // Fix 5: clean up on engine start failure
    if (![engine startAndReturnError:&err]) {
        NSLog(@"[AVFoundationJNI] engine start failed: %@", err);
        [[engine inputNode] removeTapOnBus:0];
        engine = nil;
        playerNode = nil;
        env->DeleteGlobalRef(gCallback);
        gCallback = NULL;
        return -1;
    }
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
    if (gCallback) { env->DeleteGlobalRef(gCallback); gCallback = NULL; }
}

JNIEXPORT jint JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_playSamples(
        JNIEnv* env, jclass cls, jfloatArray samples, jint sampleRate) {
    if (!playerNode || !engine) return -1;

    jsize  len  = env->GetArrayLength(samples);
    jfloat* src = env->GetFloatArrayElements(samples, NULL);

    AVAudioFormat* fmt = [[AVAudioFormat alloc]
        initWithCommonFormat:AVAudioPCMFormatFloat32
                  sampleRate:(double)sampleRate
                    channels:1
                 interleaved:NO];
    AVAudioPCMBuffer* buf = [[AVAudioPCMBuffer alloc]
        initWithPCMFormat:fmt frameCapacity:(AVAudioFrameCount)len];
    buf.frameLength = (AVAudioFrameCount)len;
    memcpy(buf.floatChannelData[0], src, len * sizeof(float));
    env->ReleaseFloatArrayElements(samples, src, JNI_ABORT);

    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    [playerNode scheduleBuffer:buf completionHandler:^{ dispatch_semaphore_signal(sem); }];
    // Fix 3: use timeout to avoid deadlock if playerNode is stopped
    dispatch_time_t timeout = dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC);
    if (dispatch_semaphore_wait(sem, timeout) != 0) return -1;
    return 0;
}

JNIEXPORT void JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_stopPlayback(
        JNIEnv* env, jclass cls) {
    if (playerNode) [playerNode stop];
}
