use graywolfmodem::audio::soundcard::{device_name, resolve_output_device, spawn, SoundcardConfig};
use std::sync::mpsc::sync_channel;
use std::time::Duration;

#[test]
fn named_pcm_reopens_and_delivers_input() {
    let saved_name = "dxb_fixture";
    let output = resolve_output_device(saved_name).expect("resolve configured output name");
    assert_eq!(device_name(&output).as_deref(), Some(saved_name));
    assert!(resolve_output_device("dxb_missing_fixture").is_err());

    for _ in 0..2 {
        let (sender, receiver) = sync_channel(8);
        let mut source = spawn(
            SoundcardConfig {
                device_name: saved_name.to_owned(),
                sample_rate: 48_000,
                channels: 1,
                audio_channel: 0,
            },
            sender,
        )
        .expect("open configured input");
        assert_eq!(source.sample_rate, 48_000);
        let samples = receiver.recv_timeout(Duration::from_secs(5));
        source.stop_and_join();
        assert!(!samples.expect("receive synthetic input").is_empty());
    }
}
